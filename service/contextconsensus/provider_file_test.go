package contextconsensus

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractProviderFileStateClassifiesSupportedProtocols(t *testing.T) {
	const (
		providerID  = "file-provider-secret"
		providerURI = "https://generativelanguage.googleapis.com/v1beta/files/provider-secret"
		signedURL   = "https://storage.example.invalid/object?X-Amz-Signature=signed-secret"
		externalURL = "https://cdn.example.invalid/public-object.png"
	)
	tests := []struct {
		name          string
		protocol      types.RelayFormat
		body          string
		expectedKinds []ProviderFileReferenceKind
		rawSecrets    []string
	}{
		{
			name:     "chat completions",
			protocol: types.RelayFormatOpenAI,
			body: `{"model":"gpt-5","messages":[{"role":"user","content":[
{"type":"file","file":{"file_id":"` + providerID + `"}},
{"type":"file","file":{"file_data":"inline-secret"}},
{"type":"image_url","image_url":{"url":"` + externalURL + `"}},
{"type":"video_url","video_url":{"url":"` + signedURL + `"}}
]}]}`,
			expectedKinds: []ProviderFileReferenceKind{ProviderFileReferenceProviderID, ProviderFileReferenceInline, ProviderFileReferenceExternalURL, ProviderFileReferenceSignedURL},
			rawSecrets:    []string{providerID, "inline-secret", externalURL, signedURL, "signed-secret"},
		},
		{
			name:     "responses",
			protocol: types.RelayFormatOpenAIResponses,
			body: `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[
{"type":"input_file","file_id":"` + providerID + `"},
{"type":"input_file","file_data":"inline-secret"},
{"type":"input_image","image_url":"` + externalURL + `"},
{"type":"input_file","file_url":"` + signedURL + `"}
]}]}`,
			expectedKinds: []ProviderFileReferenceKind{ProviderFileReferenceProviderID, ProviderFileReferenceInline, ProviderFileReferenceExternalURL, ProviderFileReferenceSignedURL},
			rawSecrets:    []string{providerID, "inline-secret", externalURL, signedURL, "signed-secret"},
		},
		{
			name:     "claude",
			protocol: types.RelayFormatClaude,
			body: `{"model":"claude-sonnet-4","messages":[{"role":"user","content":[
{"type":"document","source":{"type":"file","file_id":"` + providerID + `"}},
{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"inline-secret"}},
{"type":"image","source":{"type":"url","url":"` + externalURL + `"}},
{"type":"image","source":{"type":"url","url":"` + signedURL + `"}}
]}]}`,
			expectedKinds: []ProviderFileReferenceKind{ProviderFileReferenceProviderID, ProviderFileReferenceInline, ProviderFileReferenceExternalURL, ProviderFileReferenceSignedURL},
			rawSecrets:    []string{providerID, "inline-secret", externalURL, signedURL, "signed-secret"},
		},
		{
			name:     "gemini",
			protocol: types.RelayFormatGemini,
			body: `{"contents":[{"role":"user","parts":[
{"inlineData":{"mimeType":"image/png","data":"inline-secret"}},
{"fileData":{"mimeType":"image/png","fileUri":"` + providerURI + `"}},
{"fileData":{"mimeType":"image/png","fileUri":"` + signedURL + `"}}
]}]}`,
			expectedKinds: []ProviderFileReferenceKind{ProviderFileReferenceInline, ProviderFileReferenceProviderURI, ProviderFileReferenceSignedURL},
			rawSecrets:    []string{providerURI, "inline-secret", signedURL, "signed-secret"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := ExtractProviderFileState(ExtractionRequest{Protocol: test.protocol, Body: []byte(test.body)})
			require.NoError(t, err)
			actualKinds := make([]ProviderFileReferenceKind, 0, len(state.References))
			for _, reference := range state.References {
				actualKinds = append(actualKinds, reference.Kind)
				if reference.Kind == ProviderFileReferenceProviderID || reference.Kind == ProviderFileReferenceProviderURI {
					assert.Len(t, reference.ReferenceHMAC, 64)
					assert.Equal(t, BindingLevelCredential, reference.RequiredBinding)
				} else {
					assert.Empty(t, reference.ReferenceHMAC)
				}
			}
			assert.Equal(t, test.expectedKinds, actualKinds)

			envelope, err := Extract(ExtractionRequest{Protocol: test.protocol, Body: []byte(test.body)})
			require.NoError(t, err)
			serializedEnvelope, err := common.Marshal(envelope)
			require.NoError(t, err)
			for _, rawSecret := range test.rawSecrets {
				assert.NotContains(t, string(serializedEnvelope), rawSecret)
			}
			assert.Equal(t, state, envelope.ProviderFileState)
		})
	}
}

func TestExtractProviderFileStateDoesNotInferFromGenericFileID(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":{"type":"metadata","file_id":"not-a-provider-file"}}]}`)

	state, err := ExtractProviderFileState(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)
	assert.Empty(t, state.References)

	envelope, err := Extract(ExtractionRequest{Protocol: types.RelayFormatOpenAI, Body: body})
	require.NoError(t, err)
	assert.Zero(t, envelope.MediaState.ProviderBoundCount)
	assert.Equal(t, BindingLevelNone, envelope.ProviderBinding.BindingLevel)
}

func TestExtractProviderFileStateRejectsInvalidOrConflictingSources(t *testing.T) {
	tests := []struct {
		name     string
		protocol types.RelayFormat
		body     string
	}{
		{
			name:     "numeric Chat file ID",
			protocol: types.RelayFormatOpenAI,
			body:     `{"messages":[{"role":"user","content":[{"type":"file","file":{"file_id":123}}]}]}`,
		},
		{
			name:     "conflicting Responses file sources",
			protocol: types.RelayFormatOpenAIResponses,
			body:     `{"input":[{"type":"input_file","file_id":"file-one","file_data":"inline"}]}`,
		},
		{
			name:     "unknown Claude document source",
			protocol: types.RelayFormatClaude,
			body:     `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"unknown"}}]}]}`,
		},
		{
			name:     "empty Gemini file URI",
			protocol: types.RelayFormatGemini,
			body:     `{"contents":[{"parts":[{"fileData":{"fileUri":""}}]}]}`,
		},
		{
			name:     "numeric Chat image URL",
			protocol: types.RelayFormatOpenAI,
			body:     `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":123}]}]}`,
		},
		{
			name:     "invalid Responses URL scheme",
			protocol: types.RelayFormatOpenAIResponses,
			body:     `{"input":[{"type":"input_image","image_url":"file:///private/secret"}]}`,
		},
		{
			name:     "missing Chat audio data",
			protocol: types.RelayFormatOpenAI,
			body:     `{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"format":"wav"}}]}]}`,
		},
		{
			name:     "conflicting Gemini data sources",
			protocol: types.RelayFormatGemini,
			body:     `{"contents":[{"parts":[{"inlineData":{"data":"inline"},"fileData":{"fileUri":"files/provider-secret"}}]}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ExtractProviderFileState(ExtractionRequest{Protocol: test.protocol, Body: []byte(test.body)})
			require.ErrorContains(t, err, "structure is invalid")
			assert.NotContains(t, err.Error(), "file-one")
			assert.NotContains(t, err.Error(), "inline")
		})
	}
}

func TestProviderFileLifecycleEvidenceValidation(t *testing.T) {
	state, err := ExtractProviderFileState(ExtractionRequest{
		Protocol: types.RelayFormatGemini,
		Body:     []byte(`{"contents":[{"parts":[{"fileData":{"fileUri":"files/provider-secret"}}]}]}`),
	})
	require.NoError(t, err)
	require.Len(t, state.References, 1)

	metadata := ProviderFileLifecycleMetadata{
		ReferenceHMAC:     state.References[0].ReferenceHMAC,
		Kind:              ProviderFileReferenceProviderURI,
		OwnershipVerified: true,
		ExpiresAtUnix:     1,
		DeletionSupported: true,
	}
	require.NoError(t, metadata.Validate())
	report := ProviderFileDeletionReport{
		ReferenceHMAC: state.References[0].ReferenceHMAC,
		Kind:          ProviderFileReferenceProviderURI,
		Status:        ProviderFileDeletionDeleted,
		DeletedAtUnix: 1,
	}
	require.NoError(t, report.Validate())

	serialized, err := common.Marshal(struct {
		Metadata ProviderFileLifecycleMetadata `json:"metadata"`
		Report   ProviderFileDeletionReport    `json:"report"`
	}{Metadata: metadata, Report: report})
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "provider-secret")

	metadata.OwnershipVerified = false
	require.Error(t, metadata.Validate())
	report.Status = ProviderFileDeletionNotFound
	report.DeletedAtUnix = 0
	require.NoError(t, report.Validate())
	report.Status = ProviderFileDeletionFailed
	require.ErrorContains(t, report.Validate(), "not terminal")

	state.ReasonCodes = nil
	require.ErrorContains(t, state.Validate(), "do not match")
}
