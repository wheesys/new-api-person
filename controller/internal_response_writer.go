package controller

import (
	"bytes"
	"errors"
	"net/http"
)

var errInternalResponseTooLarge = errors.New("internal response exceeds configured limit")

type boundedInternalResponseWriter struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
	maxBytes   int64
	overflowed bool
}

func newBoundedInternalResponseWriter(maxBytes int64) *boundedInternalResponseWriter {
	return &boundedInternalResponseWriter{
		header:   make(http.Header),
		maxBytes: maxBytes,
	}
}

func (writer *boundedInternalResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *boundedInternalResponseWriter) WriteHeader(statusCode int) {
	if writer.statusCode == 0 {
		writer.statusCode = statusCode
	}
}

func (writer *boundedInternalResponseWriter) Write(data []byte) (int, error) {
	if writer.statusCode == 0 {
		writer.statusCode = http.StatusOK
	}
	if writer.maxBytes <= 0 || int64(writer.body.Len()+len(data)) > writer.maxBytes {
		writer.overflowed = true
		return 0, errInternalResponseTooLarge
	}
	return writer.body.Write(data)
}

func (writer *boundedInternalResponseWriter) Flush() {}

func (writer *boundedInternalResponseWriter) Bytes() []byte {
	return writer.body.Bytes()
}

func (writer *boundedInternalResponseWriter) Overflowed() bool {
	return writer.overflowed
}
