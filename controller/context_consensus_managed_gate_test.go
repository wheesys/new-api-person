package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateManagedContextRelayGatePreservesStatelessRequests(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, validateManagedContextRelayGate(context, false))
}

func TestValidateManagedContextRelayGateFailsClosedBeforeTransactionIntegration(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(context, constant.ContextKeyManagedContextRequest, contextconsensus.ManagedContextRequest{
		ExternalContextID: "request-local-only",
		ExpectedRevision:  1,
	})

	streamError := validateManagedContextRelayGate(context, true)
	require.NotNil(t, streamError)
	assert.Equal(t, http.StatusBadRequest, streamError.StatusCode)
	assert.Contains(t, streamError.Error(), "does not support streaming")

	nonStreamError := validateManagedContextRelayGate(context, false)
	require.NotNil(t, nonStreamError)
	assert.Equal(t, http.StatusServiceUnavailable, nonStreamError.StatusCode)
	assert.Contains(t, nonStreamError.Error(), "managed context is unavailable")
}
