package cloudflare

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestConvertCf2CompletionsRequestPreservesOutputLimitPresence(t *testing.T) {
	explicitZero := uint(0)
	legacyLimit := uint(12)

	tests := []struct {
		name          string
		request       dto.GeneralOpenAIRequest
		expectPresent bool
		expectedValue int64
	}{
		{name: "missing", request: dto.GeneralOpenAIRequest{}},
		{name: "explicit zero", request: dto.GeneralOpenAIRequest{MaxTokens: &explicitZero}, expectPresent: true},
		{name: "completion zero wins", request: dto.GeneralOpenAIRequest{MaxTokens: &legacyLimit, MaxCompletionTokens: &explicitZero}, expectPresent: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converted := convertCf2CompletionsRequest(test.request)
			encoded, err := common.Marshal(converted)
			require.NoError(t, err)

			field := gjson.GetBytes(encoded, "max_tokens")
			assert.Equal(t, test.expectPresent, field.Exists())
			if test.expectPresent {
				assert.Equal(t, test.expectedValue, field.Int())
			}
		})
	}
}
