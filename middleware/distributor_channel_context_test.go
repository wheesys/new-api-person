package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupContextForSelectedChannelClearsOpenAIScopeOnRetry(t *testing.T) {
	contextValue, _ := gin.CreateTestContext(httptest.NewRecorder())
	firstChannel := &model.Channel{
		Id: 1, Type: constant.ChannelTypeOpenAI, Name: "first", Key: "sk-first",
		OpenAIOrganization: common.GetPointer("org-first"), OpenAIProject: common.GetPointer("proj-first"),
	}
	require.Nil(t, setupContextForSelectedChannel(contextValue, firstChannel, "gpt-5", firstChannel.Key, 0))
	assert.Equal(t, "org-first", common.GetContextKeyString(contextValue, constant.ContextKeyChannelOrganization))
	assert.Equal(t, "proj-first", common.GetContextKeyString(contextValue, constant.ContextKeyChannelProject))

	retryChannel := &model.Channel{Id: 2, Type: constant.ChannelTypeOpenAI, Name: "retry", Key: "sk-retry"}
	require.Nil(t, setupContextForSelectedChannel(contextValue, retryChannel, "gpt-5", retryChannel.Key, 0))
	assert.Empty(t, common.GetContextKeyString(contextValue, constant.ContextKeyChannelOrganization))
	assert.Empty(t, common.GetContextKeyString(contextValue, constant.ContextKeyChannelProject))
}
