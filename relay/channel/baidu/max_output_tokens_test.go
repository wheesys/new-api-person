package baidu

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRequestOpenAI2BaiduPreservesOutputLimitPresence(t *testing.T) {
	legacyLimit := uint(9)
	explicitZero := uint(0)
	one := uint(1)

	tests := []struct {
		name          string
		request       dto.GeneralOpenAIRequest
		expectPresent bool
		expectedValue int64
	}{
		{name: "missing", request: dto.GeneralOpenAIRequest{}},
		{name: "explicit zero", request: dto.GeneralOpenAIRequest{MaxTokens: &explicitZero}, expectPresent: true},
		{name: "completion zero wins", request: dto.GeneralOpenAIRequest{MaxTokens: &legacyLimit, MaxCompletionTokens: &explicitZero}, expectPresent: true},
		{name: "provider minimum", request: dto.GeneralOpenAIRequest{MaxTokens: &one}, expectPresent: true, expectedValue: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converted := requestOpenAI2Baidu(test.request)
			encoded, err := common.Marshal(converted)
			require.NoError(t, err)

			field := gjson.GetBytes(encoded, "max_output_tokens")
			assert.Equal(t, test.expectPresent, field.Exists())
			if test.expectPresent {
				assert.Equal(t, test.expectedValue, field.Int())
			}
		})
	}
}
