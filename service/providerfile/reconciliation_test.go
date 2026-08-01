package providerfile

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconciliationScanPersistsOnlyCompleteStableHMACCandidates(t *testing.T) {
	runtime := prepareProviderFileLifecycleTest(t)
	fixedNow := time.Now().UTC().Truncate(time.Second)
	settings := reconciliationTestSettings()
	pages := map[string][]byte{
		"": reconciliationListBody(t, true, []map[string]any{
			reconciliationListItem("file-pre-attestation", "pre-secret.txt", 1, fixedNow.Add(-2*time.Hour), fixedNow.Add(10*time.Hour)),
			reconciliationListItem("file-quarantined", "quarantine-secret.txt", 2, fixedNow.Add(-30*time.Minute), fixedNow.Add(48*time.Hour)),
		}),
		"file-quarantined": reconciliationListBody(t, false, []map[string]any{
			reconciliationListItem("file-await-expiry", "expiry-secret.txt", 3, fixedNow.Add(-20*time.Minute), fixedNow.Add(time.Hour)),
			reconciliationListItem("file-no-expiry", "ambiguous-secret.txt", 4, fixedNow.Add(-15*time.Minute), time.Time{}),
			reconciliationListItem("file-too-new", "new-secret.txt", 5, fixedNow.Add(-time.Minute), fixedNow.Add(48*time.Hour)),
		}),
	}
	httpClient := &http.Client{Transport: providerFileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, "proj-exclusive", request.Header.Get("OpenAI-Project"))
		assert.Equal(t, "user_data", request.URL.Query().Get("purpose"))
		assert.Equal(t, "asc", request.URL.Query().Get("order"))
		body, found := pages[request.URL.Query().Get("after")]
		require.True(t, found)
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
		}, nil
	})}

	options := ReconciliationOptions{Runtime: runtime, Settings: settings, HTTPClient: httpClient, Now: func() time.Time { return fixedNow }}
	firstSummary, err := RunReconciliationScan(context.Background(), options)
	require.NoError(t, err)
	assert.Equal(t, ReconciliationSummary{
		ScanID: firstSummary.ScanID, State: model.ManagedProviderFileReconciliationScanStateComplete,
		PageCount: 2, ObjectCount: 5, CandidateCount: 4,
	}, firstSummary)

	var candidates []model.ManagedProviderFileReconciliationCandidate
	require.NoError(t, model.DB.Order("id ASC").Find(&candidates).Error)
	require.Len(t, candidates, 4)
	assert.Equal(t, []string{
		model.ManagedProviderFileReconciliationCandidateExcludedPreAttestation,
		model.ManagedProviderFileReconciliationCandidateQuarantined,
		model.ManagedProviderFileReconciliationCandidateAwaitExpiry,
		model.ManagedProviderFileReconciliationCandidateAmbiguous,
	}, []string{candidates[0].State, candidates[1].State, candidates[2].State, candidates[3].State})
	quarantineUntil := candidates[1].QuarantineUntil
	require.NotNil(t, quarantineUntil)
	assert.Equal(t, fixedNow.Add(24*time.Hour), *quarantineUntil)

	secondSummary, err := RunReconciliationScan(context.Background(), options)
	require.NoError(t, err)
	assert.Equal(t, 4, secondSummary.CandidateCount)
	require.NoError(t, model.DB.Order("id ASC").Find(&candidates).Error)
	for _, candidate := range candidates {
		assert.Equal(t, 2, candidate.CompleteObservationCount)
	}
	assert.Equal(t, quarantineUntil, candidates[1].QuarantineUntil)

	serialized, err := common.Marshal(candidates)
	require.NoError(t, err)
	for _, sensitiveValue := range []string{"file-pre-attestation", "file-quarantined", "quarantine-secret.txt", "ambiguous-secret.txt"} {
		assert.NotContains(t, string(serialized), sensitiveValue)
		assert.NotContains(t, fmt.Sprintf("%#v", firstSummary), sensitiveValue)
	}
	payload, err := runtime.DecryptProviderFileReconciliationReference(context.Background(), candidates[1].TargetProviderLookupHMAC, candidates[1].ProviderPayload)
	require.NoError(t, err)
	assert.Equal(t, "file-quarantined", payload.ProviderFileID)
}

func TestReconciliationIncompletePageDoesNotAdvanceCandidates(t *testing.T) {
	runtime := prepareProviderFileLifecycleTest(t)
	fixedNow := time.Now().UTC().Truncate(time.Second)
	body, err := common.Marshal(map[string]any{
		"object": "list", "data": []any{}, "first_id": "", "last_id": "", "has_more": true,
	})
	require.NoError(t, err)
	httpClient := &http.Client{Transport: providerFileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
		}, nil
	})}

	summary, err := RunReconciliationScan(context.Background(), ReconciliationOptions{
		Runtime: runtime, Settings: reconciliationTestSettings(), HTTPClient: httpClient, Now: func() time.Time { return fixedNow },
	})
	assert.ErrorIs(t, err, ErrReconciliationUnavailable)
	assert.Equal(t, model.ManagedProviderFileReconciliationScanStateIncomplete, summary.State)
	assert.Equal(t, "cursor_no_progress", summary.FailureCode)
	var candidateCount int64
	require.NoError(t, model.DB.Model(&model.ManagedProviderFileReconciliationCandidate{}).Count(&candidateCount).Error)
	assert.Zero(t, candidateCount)
	var scan model.ManagedProviderFileReconciliationScan
	require.NoError(t, model.DB.First(&scan, "id = ?", summary.ScanID).Error)
	assert.Equal(t, model.ManagedProviderFileReconciliationScanStateIncomplete, scan.State)
	assert.Equal(t, "cursor_no_progress", scan.FailureCode)
}

func TestReconciliationLaterPageFailureDoesNotPersistEarlierObservations(t *testing.T) {
	runtime := prepareProviderFileLifecycleTest(t)
	fixedNow := time.Now().UTC().Truncate(time.Second)
	firstPage := reconciliationListBody(t, true, []map[string]any{
		reconciliationListItem("file-observed-before-failure", "failure-secret.txt", 1,
			fixedNow.Add(-time.Hour), fixedNow.Add(48*time.Hour)),
	})
	httpClient := &http.Client{Transport: providerFileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		statusCode := http.StatusOK
		body := firstPage
		if request.URL.Query().Get("after") != "" {
			statusCode = http.StatusInternalServerError
			body = []byte(`{"error":"temporary"}`)
		}
		return &http.Response{
			StatusCode: statusCode, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
		}, nil
	})}
	settings := reconciliationTestSettings()
	settings.ProviderFileLifecycleEnabled = false

	summary, err := RunReconciliationScan(context.Background(), ReconciliationOptions{
		Runtime: runtime, Settings: settings, HTTPClient: httpClient, Now: func() time.Time { return fixedNow },
	})
	assert.ErrorIs(t, err, ErrReconciliationUnavailable)
	assert.Equal(t, model.ManagedProviderFileReconciliationScanStateIncomplete, summary.State)
	assert.Equal(t, "provider_list_failed", summary.FailureCode)
	assert.Equal(t, 1, summary.ObjectCount)
	var candidateCount int64
	require.NoError(t, model.DB.Model(&model.ManagedProviderFileReconciliationCandidate{}).Count(&candidateCount).Error)
	assert.Zero(t, candidateCount)
}

func TestReconciliationObjectCapTerminalStateAcceptsExactBound(t *testing.T) {
	prepareProviderFileLifecycleTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	scan := &model.ManagedProviderFileReconciliationScan{
		TargetFingerprint: strings.Repeat("1", 64), ScopeFingerprint: strings.Repeat("2", 64), KeyVersion: "provider-file-v1",
		State: model.ManagedProviderFileReconciliationScanStateScanning, Version: 1, StartedAt: now, CutoffAt: now.Add(-time.Minute),
	}
	require.NoError(t, model.CreateManagedProviderFileReconciliationScan(context.Background(), scan))
	require.NoError(t, model.FinishManagedProviderFileReconciliationScan(context.Background(), scan,
		model.ManagedProviderFileReconciliationScanStateIncomplete, "object_cap_reached", "", 100,
		providerFileReconciliationMaximumObjects, nil, now.Add(time.Second)))

	var persisted model.ManagedProviderFileReconciliationScan
	require.NoError(t, model.DB.First(&persisted, "id = ?", scan.Id).Error)
	assert.Equal(t, model.ManagedProviderFileReconciliationScanStateIncomplete, persisted.State)
	assert.Equal(t, providerFileReconciliationMaximumObjects, persisted.ObjectCount)
	assert.Equal(t, "object_cap_reached", persisted.FailureCode)
}

func reconciliationTestSettings() *model_setting.SmartRoutingSettings {
	return &model_setting.SmartRoutingSettings{
		ProviderFileLifecycleEnabled: true, ProviderFileOpenAIChannelID: 41, ProviderFileExpirationSeconds: 3600,
		ProviderFileMetadataVerifyTTLSeconds: 0, ProviderFileDeletionLeadSeconds: 60, ProviderFileDeletionBatchSize: 10,
		ProviderFileDeletionMaxAttempts: 3, ProviderFileDeletionTimeoutSeconds: 5,
		ProviderFileExclusiveProjectAttested: true, ProviderFileSandboxContractVerified: true, ProviderFileReconciliationEnabled: true,
	}
}

func reconciliationListItem(providerFileID, filename string, bytes int64, createdAt, expiresAt time.Time) map[string]any {
	expiresAtUnix := int64(0)
	if !expiresAt.IsZero() {
		expiresAtUnix = expiresAt.Unix()
	}
	return map[string]any{
		"id": providerFileID, "object": "file", "bytes": bytes, "created_at": createdAt.Unix(),
		"expires_at": expiresAtUnix, "filename": filename, "purpose": "user_data",
	}
}

func reconciliationListBody(t *testing.T, hasMore bool, items []map[string]any) []byte {
	t.Helper()
	firstID := ""
	lastID := ""
	if len(items) > 0 {
		firstID, _ = items[0]["id"].(string)
		lastID, _ = items[len(items)-1]["id"].(string)
	}
	body, err := common.Marshal(map[string]any{
		"object": "list", "data": items, "first_id": firstID, "last_id": lastID, "has_more": hasMore,
	})
	require.NoError(t, err)
	return body
}
