package router

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedProviderFileRoutesUseDedicatedHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)
	routes := engine.Routes()
	handlers := make(map[string]string, len(routes))
	for _, route := range routes {
		handlers[route.Method+" "+route.Path] = route.Handler
	}
	require.Contains(t, handlers, http.MethodPost+" /v1/files")
	assert.True(t, strings.HasSuffix(handlers[http.MethodPost+" /v1/files"], ".UploadManagedProviderFile"))
	require.Contains(t, handlers, http.MethodGet+" /v1/files/:id")
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /v1/files/:id"], ".RetrieveManagedProviderFile"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /v1/files"], ".RelayNotImplemented"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodDelete+" /v1/files/:id"], ".RelayNotImplemented"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /v1/files/:id/content"], ".RelayNotImplemented"))
}
