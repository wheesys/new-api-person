package contextconsensus

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractManagedProviderStateReportAcceptsOnlyCompleteNativeResponses(t *testing.T) {
	responseID := "resp_provider_state_secret"
	report, err := ExtractManagedProviderStateReport(ExtractManagedProviderStateReportRequest{
		SourceProtocol: types.RelayFormatOpenAIResponses,
		FinalProtocol:  types.RelayFormatOpenAIResponses,
		HTTPStatus:     200,
		ResponseBody:   []byte(fmt.Sprintf(`{"id":%q,"object":"response","status":"completed","error":null,"incomplete_details":null}`, responseID)),
	})
	require.NoError(t, err)
	assert.Equal(t, responseID, report.StateReference)
	assert.Equal(t, managedOpenAIResponseStateField, report.RequestField)
	assert.Equal(t, managedOpenAIResponseStateReason, report.ReasonCode)

	encoded, err := common.Marshal(report)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), responseID)
	assert.NotContains(t, fmt.Sprintf("%v", report), responseID)
	assert.NotContains(t, fmt.Sprintf("%#v", report), responseID)

	tests := []struct {
		name    string
		request ExtractManagedProviderStateReportRequest
	}{
		{
			name: "converted source protocol",
			request: ExtractManagedProviderStateReportRequest{
				SourceProtocol: types.RelayFormatOpenAI,
				FinalProtocol:  types.RelayFormatOpenAIResponses,
				HTTPStatus:     200,
				ResponseBody:   []byte(`{"id":"resp_converted","status":"completed"}`),
			},
		},
		{
			name: "converted final protocol",
			request: ExtractManagedProviderStateReportRequest{
				SourceProtocol: types.RelayFormatOpenAIResponses,
				FinalProtocol:  types.RelayFormatOpenAI,
				HTTPStatus:     200,
				ResponseBody:   []byte(`{"id":"resp_converted","status":"completed"}`),
			},
		},
		{
			name: "upstream error",
			request: ExtractManagedProviderStateReportRequest{
				SourceProtocol: types.RelayFormatOpenAIResponses,
				FinalProtocol:  types.RelayFormatOpenAIResponses,
				HTTPStatus:     502,
				ResponseBody:   []byte(`{"id":"resp_failed","status":"completed"}`),
			},
		},
		{
			name: "incomplete response",
			request: ExtractManagedProviderStateReportRequest{
				SourceProtocol: types.RelayFormatOpenAIResponses,
				FinalProtocol:  types.RelayFormatOpenAIResponses,
				HTTPStatus:     200,
				ResponseBody:   []byte(`{"id":"resp_incomplete","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}`),
			},
		},
		{
			name: "missing response id",
			request: ExtractManagedProviderStateReportRequest{
				SourceProtocol: types.RelayFormatOpenAIResponses,
				FinalProtocol:  types.RelayFormatOpenAIResponses,
				HTTPStatus:     200,
				ResponseBody:   []byte(`{"status":"completed"}`),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ExtractManagedProviderStateReport(test.request)
			require.Error(t, err)
		})
	}
}

func TestManagedProviderStateBindingRegistersEncryptedAndPinsExactTarget(t *testing.T) {
	now := time.Now()
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	runtime := managedProviderStateTestRuntime(t, "v1", "a", nil, repository)
	owner := ManagedConsensusOwner{UserID: 7, TokenID: 11, EndpointFamily: "responses"}
	stateReference := "resp_owner_scoped_secret"
	credential := "provider-credential-secret"
	target := ManagedProviderFinalTarget{
		RelayFormat:       types.RelayFormatOpenAIResponses,
		ChannelID:         23,
		ChannelType:       1,
		OriginModel:       "gpt-public",
		UpstreamModel:     "gpt-5.1",
		MultiKeyIndex:     2,
		ChannelIsMultiKey: true,
		Credential:        credential,
	}

	commit := prepareAndRegisterManagedProviderStateForTest(t, runtime, repository, owner, "provider-binding-context", "provider-binding-holder", ManagedProviderStateReport{
		Version: ManagedProviderStateReportVersion, SourceProtocol: types.RelayFormatOpenAIResponses,
		FinalProtocol: types.RelayFormatOpenAIResponses, RequestField: managedOpenAIResponseStateField,
		ReasonCode: managedOpenAIResponseStateReason, StateReference: stateReference,
	}, target, now)
	assert.NotEmpty(t, commit.Binding.Target.CredentialFingerprint)
	assert.NotEqual(t, credential, commit.Binding.Target.CredentialFingerprint)

	record := repository.providerStateBindings[commit.StorageKey.RepositoryKey]
	encodedRecord, err := common.Marshal(record)
	require.NoError(t, err)
	assert.NotContains(t, commit.StorageKey.RepositoryKey, stateReference)
	assert.NotContains(t, string(encodedRecord), stateReference)
	assert.NotContains(t, string(encodedRecord), credential)

	resolution, err := ResolveManagedProviderStateBinding(context.Background(), runtime, owner, stateReference)
	require.NoError(t, err)
	require.NoError(t, resolution.ValidateFinalTarget(target))
	assert.Equal(t, target.ChannelID, resolution.Target().ChannelID)
	assert.Equal(t, target.MultiKeyIndex, resolution.Target().MultiKeyIndex)

	wrongCredential := target
	wrongCredential.Credential = "rotated-provider-credential"
	require.ErrorIs(t, resolution.ValidateFinalTarget(wrongCredential), ErrProviderStateBindingConflict)
	wrongSlot := target
	wrongSlot.MultiKeyIndex++
	require.ErrorIs(t, resolution.ValidateFinalTarget(wrongSlot), ErrProviderStateBindingConflict)
	wrongModel := target
	wrongModel.UpstreamModel = "gpt-5.2"
	require.ErrorIs(t, resolution.ValidateFinalTarget(wrongModel), ErrProviderStateBindingConflict)
	wrongOriginModel := target
	wrongOriginModel.OriginModel = "gpt-other-alias"
	require.ErrorIs(t, resolution.ValidateFinalTarget(wrongOriginModel), ErrProviderStateBindingConflict)
	require.NoError(t, resolution.ValidateStateReference(stateReference))
	require.ErrorIs(t, resolution.ValidateStateReference("resp_other_state"), ErrProviderStateBindingConflict)

	_, err = ResolveManagedProviderStateBinding(context.Background(), runtime, ManagedConsensusOwner{
		UserID: owner.UserID, TokenID: owner.TokenID + 1, EndpointFamily: owner.EndpointFamily,
	}, stateReference)
	require.ErrorIs(t, err, ErrProviderStateBindingNotFound)
}

func TestManagedProviderStateBindingReadsPreviousKeyAndRejectsDualNamespace(t *testing.T) {
	now := time.Now()
	repository := newMemoryManagedConsensusRepository(func() time.Time { return now })
	previousRuntime := managedProviderStateTestRuntime(t, "v1", "p", nil, repository)
	activeRuntime := managedProviderStateTestRuntime(t, "v2", "a", nil, repository)
	rotatingRuntime := managedProviderStateTestRuntime(t, "v2", "a", []managedConsensusPreviousKey{{
		Version: "v1",
		Key:     base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))),
	}}, repository)
	owner := ManagedConsensusOwner{UserID: 4, TokenID: 9, EndpointFamily: "responses"}
	stateReference := "resp_rotating_state"
	target := ManagedProviderFinalTarget{
		RelayFormat: types.RelayFormatOpenAIResponses, ChannelID: 31, ChannelType: 1,
		OriginModel: "gpt-public", UpstreamModel: "gpt-5", MultiKeyIndex: 0, Credential: "previous-credential",
	}
	report := ManagedProviderStateReport{
		Version: ManagedProviderStateReportVersion, SourceProtocol: types.RelayFormatOpenAIResponses,
		FinalProtocol: types.RelayFormatOpenAIResponses, RequestField: managedOpenAIResponseStateField,
		ReasonCode: managedOpenAIResponseStateReason, StateReference: stateReference,
	}
	prepareAndRegisterManagedProviderStateForTest(t, previousRuntime, repository, owner, "previous-provider-context", "previous-provider-holder", report, target, now)

	resolution, err := ResolveManagedProviderStateBinding(context.Background(), rotatingRuntime, owner, stateReference)
	require.NoError(t, err)
	require.NoError(t, resolution.ValidateFinalTarget(target))

	prepareAndRegisterManagedProviderStateForTest(t, activeRuntime, repository, owner, "active-provider-context", "active-provider-holder", report, target, now)
	_, err = ResolveManagedProviderStateBinding(context.Background(), rotatingRuntime, owner, stateReference)
	require.ErrorIs(t, err, ErrManagedConsensusKeyConflict)
}

func prepareAndRegisterManagedProviderStateForTest(
	t *testing.T,
	runtime *ManagedConsensusRuntime,
	repository ManagedConsensusRepository,
	owner ManagedConsensusOwner,
	externalContextID string,
	holderID string,
	report ManagedProviderStateReport,
	target ManagedProviderFinalTarget,
	now time.Time,
) ManagedProviderStateCommit {
	t.Helper()
	ctx := context.Background()
	session, err := BeginManagedConsensusSession(ctx, runtime, BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: externalContextID,
		ExpectedRevision:  0,
		HolderID:          holderID,
		LeaseTTL:          time.Minute,
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, session.Close(ctx))
	}()

	commit, err := session.PrepareProviderStateCommitForOwner(ctx, owner, report, target, time.Hour, now)
	require.NoError(t, err)
	_, err = repository.RegisterProviderStateBinding(ctx, commit.StorageKey, commit.BindingDigest, commit.Payload, time.Hour)
	require.NoError(t, err)
	return commit
}

func managedProviderStateTestRuntime(
	t *testing.T,
	version string,
	keyCharacter string,
	previousKeys []managedConsensusPreviousKey,
	repository ManagedConsensusRepository,
) *ManagedConsensusRuntime {
	t.Helper()
	runtime, err := newManagedConsensusRuntime(
		[]byte(strings.Repeat(keyCharacter, 32)),
		version,
		previousKeys,
		repository,
	)
	require.NoError(t, err)
	return runtime
}
