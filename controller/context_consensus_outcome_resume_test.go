package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestWriteManagedOutcomeResponseUsesOnlyGatewaySafeHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	contextValue, _ := gin.CreateTestContext(recorder)
	contextValue.Header("Set-Cookie", "secret=value")
	contextValue.Header("X-Request-Id", "upstream-request")
	writeManagedOutcomeResponse(contextValue, 8, contextconsensus.ManagedOutcomeResponse{
		Status: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`),
	})

	assert.Equal(t, 201, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "11", recorder.Header().Get("Content-Length"))
	assert.Equal(t, "8", recorder.Header().Get("X-New-Api-Context-Revision"))
	assert.Empty(t, recorder.Header().Get("Set-Cookie"))
	assert.Empty(t, recorder.Header().Get("X-Request-Id"))
}
