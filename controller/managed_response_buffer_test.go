package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedResponseBufferDelaysHeadersStatusAndBodyUntilCommit(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Writer.Header().Set("X-Existing", "preserved")
	buffer, err := newManagedResponseBuffer(context.Writer, 1024)
	require.NoError(t, err)

	buffer.Header().Set("Content-Type", "application/json")
	buffer.WriteHeader(http.StatusCreated)
	written, err := buffer.WriteString(`{"ok":true}`)
	require.NoError(t, err)
	assert.Equal(t, len(`{"ok":true}`), written)
	assert.Equal(t, http.StatusCreated, buffer.Status())
	assert.Equal(t, len(`{"ok":true}`), buffer.Size())
	assert.Empty(t, recorder.Body.String())
	assert.Equal(t, http.StatusOK, recorder.Code)

	require.NoError(t, buffer.FlushToClient())
	assert.Equal(t, http.StatusCreated, recorder.Code)
	assert.JSONEq(t, `{"ok":true}`, recorder.Body.String())
	assert.Equal(t, "preserved", recorder.Header().Get("X-Existing"))
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.ErrorIs(t, buffer.FlushToClient(), errManagedResponseAlreadyWritten)
	assert.JSONEq(t, `{"ok":true}`, recorder.Body.String())
}

func TestManagedResponseBufferFailsClosedOnOverflowWithoutPartialClientWrite(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	buffer, err := newManagedResponseBuffer(context.Writer, 4)
	require.NoError(t, err)

	_, err = buffer.Write([]byte("12345"))
	require.ErrorIs(t, err, errManagedResponseBufferOverflow)
	_, err = buffer.Body()
	require.ErrorIs(t, err, errManagedResponseBufferOverflow)
	require.ErrorIs(t, buffer.FlushToClient(), errManagedResponseBufferOverflow)
	assert.Empty(t, recorder.Body.String())
}

func TestManagedResponseBufferRejectsFlushAndHijack(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	buffer, err := newManagedResponseBuffer(context.Writer, 1024)
	require.NoError(t, err)

	buffer.Flush()
	_, err = buffer.Body()
	require.ErrorIs(t, err, errManagedResponseBufferFlushed)
	require.ErrorIs(t, buffer.FlushToClient(), errManagedResponseBufferFlushed)
	connection, readWriter, err := buffer.Hijack()
	require.Error(t, err)
	assert.Nil(t, connection)
	assert.Nil(t, readWriter)
	assert.Empty(t, recorder.Body.String())
}
