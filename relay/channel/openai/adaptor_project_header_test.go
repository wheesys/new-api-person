package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupRequestHeaderSendsConfiguredOpenAIProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	contextValue, _ := gin.CreateTestContext(httptest.NewRecorder())
	contextValue.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	header := http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeOpenAI, ApiKey: "sk-secret", Organization: "org-exclusive", Project: "proj-exclusive",
	}}

	require.NoError(t, (&Adaptor{}).SetupRequestHeader(contextValue, &header, info))
	assert.Equal(t, "org-exclusive", header.Get("OpenAI-Organization"))
	assert.Equal(t, "proj-exclusive", header.Get("OpenAI-Project"))
	assert.Equal(t, "Bearer sk-secret", header.Get("Authorization"))
	formatted := info.ToString()
	assert.NotContains(t, formatted, "org-exclusive")
	assert.NotContains(t, formatted, "proj-exclusive")
}
