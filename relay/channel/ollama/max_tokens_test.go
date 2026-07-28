package ollama

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaConvertersPreserveOutputLimitPresence(t *testing.T) {
	explicitZero := uint(0)
	legacyLimit := uint(20)

	tests := []struct {
		name          string
		request       dto.GeneralOpenAIRequest
		expectPresent bool
		expectedValue int
	}{
		{name: "missing", request: dto.GeneralOpenAIRequest{}},
		{name: "explicit zero", request: dto.GeneralOpenAIRequest{MaxTokens: &explicitZero}, expectPresent: true},
		{name: "completion zero wins", request: dto.GeneralOpenAIRequest{MaxTokens: &legacyLimit, MaxCompletionTokens: &explicitZero}, expectPresent: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chatRequest, err := openAIChatToOllamaChat(nil, &test.request)
			require.NoError(t, err)
			generateRequest, err := openAIToGenerate(nil, &test.request)
			require.NoError(t, err)

			chatValue, chatPresent := chatRequest.Options["num_predict"]
			generateValue, generatePresent := generateRequest.Options["num_predict"]
			assert.Equal(t, test.expectPresent, chatPresent)
			assert.Equal(t, test.expectPresent, generatePresent)
			if test.expectPresent {
				assert.Equal(t, test.expectedValue, chatValue)
				assert.Equal(t, test.expectedValue, generateValue)
			}
		})
	}
}
