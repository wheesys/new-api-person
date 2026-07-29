package controller

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	errManagedResponseBufferOverflow = errors.New("managed response exceeds buffer limit")
	errManagedResponseBufferFlushed  = errors.New("managed non-stream response attempted to flush")
	errManagedResponseAlreadyWritten = errors.New("managed response has already been written to client")
)

type managedResponseBuffer struct {
	parent      gin.ResponseWriter
	header      http.Header
	body        bytes.Buffer
	maximumSize int
	status      int
	size        int
	overflowed  bool
	flushed     bool
	committed   bool
}

func newManagedResponseBuffer(parent gin.ResponseWriter, maximumSize int) (*managedResponseBuffer, error) {
	if parent == nil {
		return nil, fmt.Errorf("managed response parent writer is required")
	}
	if maximumSize <= 0 {
		return nil, fmt.Errorf("managed response buffer limit must be positive")
	}
	return &managedResponseBuffer{
		parent:      parent,
		header:      parent.Header().Clone(),
		maximumSize: maximumSize,
		status:      http.StatusOK,
		size:        -1,
	}, nil
}

func (writer *managedResponseBuffer) Header() http.Header {
	return writer.header
}

func (writer *managedResponseBuffer) WriteHeader(statusCode int) {
	if statusCode <= 0 || writer.Written() {
		return
	}
	writer.status = statusCode
}

func (writer *managedResponseBuffer) WriteHeaderNow() {
	if writer.size < 0 {
		writer.size = 0
	}
}

func (writer *managedResponseBuffer) Write(data []byte) (int, error) {
	if writer.overflowed {
		return 0, errManagedResponseBufferOverflow
	}
	writer.WriteHeaderNow()
	if len(data) > writer.maximumSize-writer.body.Len() {
		writer.overflowed = true
		return 0, errManagedResponseBufferOverflow
	}
	written, err := writer.body.Write(data)
	writer.size += written
	return written, err
}

func (writer *managedResponseBuffer) WriteString(data string) (int, error) {
	return writer.Write([]byte(data))
}

func (writer *managedResponseBuffer) Status() int {
	return writer.status
}

func (writer *managedResponseBuffer) Size() int {
	return writer.size
}

func (writer *managedResponseBuffer) Written() bool {
	return writer.size >= 0
}

func (writer *managedResponseBuffer) Flush() {
	writer.WriteHeaderNow()
	writer.flushed = true
}

func (writer *managedResponseBuffer) CloseNotify() <-chan bool {
	return writer.parent.CloseNotify()
}

func (writer *managedResponseBuffer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("managed response buffering does not support connection hijacking")
}

func (writer *managedResponseBuffer) Pusher() http.Pusher {
	return nil
}

func (writer *managedResponseBuffer) Body() ([]byte, error) {
	if writer.overflowed {
		return nil, errManagedResponseBufferOverflow
	}
	if writer.flushed {
		return nil, errManagedResponseBufferFlushed
	}
	return append([]byte(nil), writer.body.Bytes()...), nil
}

func (writer *managedResponseBuffer) FlushToClient() error {
	if writer.committed {
		return errManagedResponseAlreadyWritten
	}
	if _, err := writer.Body(); err != nil {
		return err
	}
	writer.committed = true
	destinationHeader := writer.parent.Header()
	for key := range destinationHeader {
		destinationHeader.Del(key)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(writer.header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" && contentType != "application/problem+json" {
		contentType = "application/json"
	}
	destinationHeader.Set("Content-Type", contentType)
	destinationHeader.Set("Content-Length", strconv.Itoa(writer.body.Len()))
	if revision := strings.TrimSpace(writer.header.Get("X-New-Api-Context-Revision")); revision != "" {
		destinationHeader.Set("X-New-Api-Context-Revision", revision)
	}
	writer.parent.WriteHeader(writer.status)
	if writer.body.Len() == 0 {
		return nil
	}
	_, err := writer.parent.Write(writer.body.Bytes())
	return err
}

var _ gin.ResponseWriter = (*managedResponseBuffer)(nil)
