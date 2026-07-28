package cohere

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRequestOpenAI2CohereDistinguishesMissingAndExplicitZero(t *testing.T) {
	explicitZero := uint(0)
	legacyLimit := uint(15)

	tests := []struct {
		name          string
		request       dto.GeneralOpenAIRequest
		expectedValue int64
	}{
		{name: "missing uses existing default", request: dto.GeneralOpenAIRequest{}, expectedValue: 4000},
		{name: "explicit zero", request: dto.GeneralOpenAIRequest{MaxTokens: &explicitZero}},
		{name: "completion zero wins", request: dto.GeneralOpenAIRequest{MaxTokens: &legacyLimit, MaxCompletionTokens: &explicitZero}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converted := requestOpenAI2Cohere(test.request)
			encoded, err := common.Marshal(converted)
			require.NoError(t, err)

			field := gjson.GetBytes(encoded, "max_tokens")
			require.True(t, field.Exists())
			assert.Equal(t, test.expectedValue, field.Int())
		})
	}
}
