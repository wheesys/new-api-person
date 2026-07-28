package channel

import (
	"io"
	"sync"
)

type firstResponseReadCloser struct {
	body io.ReadCloser
	once sync.Once
	mark func()
}

func newFirstResponseReadCloser(body io.ReadCloser, mark func()) io.ReadCloser {
	return &firstResponseReadCloser{
		body: body,
		mark: mark,
	}
}

func (reader *firstResponseReadCloser) Read(buffer []byte) (int, error) {
	readCount, err := reader.body.Read(buffer)
	if readCount > 0 && reader.mark != nil {
		reader.once.Do(reader.mark)
	}
	return readCount, err
}

func (reader *firstResponseReadCloser) Close() error {
	return reader.body.Close()
}
