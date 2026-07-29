package controller

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateManagedContextRelayGatePreservesStatelessRequests(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, validateManagedContextRelayGate(context, false))
}

func TestValidateManagedContextRelayGateRejectsOnlyStreaming(t *testing.T) {
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
	require.Nil(t, nonStreamError)
}

func TestValidateManagedContextRelayGateRejectsRevisionOverflowBeforeExecution(t *testing.T) {
	contextValue, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(contextValue, constant.ContextKeyManagedContextRequest, contextconsensus.ManagedContextRequest{
		ExternalContextID: "request-local-only",
		ExpectedRevision:  math.MaxUint64,
	})

	gateError := validateManagedContextRelayGate(contextValue, false)
	require.NotNil(t, gateError)
	assert.Equal(t, http.StatusConflict, gateError.StatusCode)
	assert.Equal(t, types.ErrorCodeManagedContextRevisionFailed, gateError.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(gateError))
}
