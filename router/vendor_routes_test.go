package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestVendorManagementRoutesAreNotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	for _, route := range engine.Routes() {
		assert.False(t, route.Method == http.MethodGet && route.Path == "/api/vendors/", "vendor list route should be removed")
		assert.False(t, route.Method == http.MethodGet && route.Path == "/api/vendors/search", "vendor search route should be removed")
		assert.False(t, route.Method == http.MethodGet && route.Path == "/api/vendors/:id", "vendor detail route should be removed")
		assert.False(t, route.Method == http.MethodPost && route.Path == "/api/vendors/", "vendor create route should be removed")
		assert.False(t, route.Method == http.MethodPut && route.Path == "/api/vendors/", "vendor update route should be removed")
		assert.False(t, route.Method == http.MethodDelete && route.Path == "/api/vendors/:id", "vendor delete route should be removed")
	}
}
