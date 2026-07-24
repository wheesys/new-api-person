package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupContextForTokenCopiesContextCompactionPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	token := &model.Token{Id: 17, UserId: 23, AllowContextCompaction: true}
	require.NoError(t, SetupContextForToken(context, token))
	assert.True(t, common.GetContextKeyBool(context, constant.ContextKeyTokenContextCompaction))

	context, _ = gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, SetupContextForToken(context, &model.Token{Id: 18, UserId: 23}))
	assert.False(t, common.GetContextKeyBool(context, constant.ContextKeyTokenContextCompaction))
}
