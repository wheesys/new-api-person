package channel

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirstResponseReadCloserMarksFirstPositiveReadOnce(t *testing.T) {
	markCount := 0
	reader := newFirstResponseReadCloser(io.NopCloser(strings.NewReader("response")), func() {
		markCount++
	})
	buffer := make([]byte, 3)

	_, err := reader.Read(buffer)
	require.NoError(t, err)
	_, err = reader.Read(buffer)
	require.NoError(t, err)
	assert.Equal(t, 1, markCount)
}

func TestFirstResponseReadCloserDoesNotMarkEmptyRead(t *testing.T) {
	markCount := 0
	reader := newFirstResponseReadCloser(io.NopCloser(strings.NewReader("")), func() {
		markCount++
	})

	readCount, err := reader.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)
	assert.Zero(t, readCount)
	assert.Zero(t, markCount)
}

func TestFirstResponseReadCloserDelegatesClose(t *testing.T) {
	expectedError := errors.New("close failed")
	body := &closeErrorReadCloser{Reader: strings.NewReader("response"), err: expectedError}
	reader := newFirstResponseReadCloser(body, nil)

	assert.ErrorIs(t, reader.Close(), expectedError)
}

type closeErrorReadCloser struct {
	io.Reader
	err error
}

func (reader *closeErrorReadCloser) Close() error {
	return reader.err
}
