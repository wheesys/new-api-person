package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedResponseBufferDelaysHeadersStatusAndBodyUntilCommit(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Writer.Header().Set("X-Existing", "preserved")
	context.Writer.Header().Set("Set-Cookie", "secret=value")
	context.Writer.Header().Set("X-Request-Id", "upstream-request")
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
	assert.Empty(t, recorder.Header().Get("X-Existing"))
	assert.Empty(t, recorder.Header().Get("Set-Cookie"))
	assert.Empty(t, recorder.Header().Get("X-Request-Id"))
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, strconv.Itoa(len(`{"ok":true}`)), recorder.Header().Get("Content-Length"))
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

func TestManagedResponseBufferAcceptsLimitAndRejectsLimitPlusOne(t *testing.T) {
	for _, test := range []struct {
		name      string
		length    int
		wantError bool
	}{
		{name: "limit minus one", length: 7},
		{name: "limit", length: 8},
		{name: "limit plus one", length: 9, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			contextValue, _ := gin.CreateTestContext(recorder)
			buffer, err := newManagedResponseBuffer(contextValue.Writer, 8)
			require.NoError(t, err)

			_, err = buffer.Write(make([]byte, test.length))
			if test.wantError {
				require.ErrorIs(t, err, errManagedResponseBufferOverflow)
				_, err = buffer.Body()
				require.ErrorIs(t, err, errManagedResponseBufferOverflow)
				assert.Empty(t, recorder.Body.Bytes())
				return
			}
			require.NoError(t, err)
			body, err := buffer.Body()
			require.NoError(t, err)
			assert.Len(t, body, test.length)
			assert.Empty(t, recorder.Body.Bytes())
		})
	}
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
