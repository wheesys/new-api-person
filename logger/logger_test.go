package logger

import (
	"bytes"
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestLogDebugSuppressesSensitiveInternalRequest(t *testing.T) {
	originalDebugEnabled := common.DebugEnabled
	common.DebugEnabled = true
	defer func() { common.DebugEnabled = originalDebugEnabled }()

	var output bytes.Buffer
	common.LogWriterMu.Lock()
	originalErrorWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	defer func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = originalErrorWriter
		common.LogWriterMu.Unlock()
	}()

	ctx := context.WithValue(context.Background(), string(constant.ContextKeySuppressDebugLog), true)
	LogDebug(ctx, "sensitive body: %s", "must-not-appear")

	assert.Empty(t, output.String())
}
