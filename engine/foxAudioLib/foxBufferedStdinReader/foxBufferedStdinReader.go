package foxBufferedStdinReader

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

type BufferedStdinReader struct {
	source  io.Reader
	buf     *bytes.Buffer
	readErr error
	closed  bool
	mutex   sync.Mutex
	closer  io.Closer // For files that need explicit closing
}

func IsStdinPipe() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) == 0
}

func NewBufferedStdinReader() *BufferedStdinReader {
	b := &BufferedStdinReader{
		buf: bytes.NewBuffer(make([]byte, 0, 65536)),
	}
	go b.continuousRead()
	return b
}

func (b *BufferedStdinReader) continuousRead() {
	tmp := make([]byte, 4096)
	for {
		n, err := b.source.Read(tmp)
		b.mutex.Lock()
		if err != nil {
			b.readErr = err
			b.mutex.Unlock()
			return
		}
		b.buf.Write(tmp[:n])
		b.mutex.Unlock()
	}
}

func (b *BufferedStdinReader) Read(p []byte) (n int, err error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.buf.Read(p)
}

func (b *BufferedStdinReader) PeekHeader(format string) ([]byte, error) {
	// LMS requires header processing within 2 seconds
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.mutex.Lock()
		switch format {
		case "WAV":
			if b.buf.Len() >= 44 { // Minimum WAV header size
				header := make([]byte, 44)
				copy(header, b.buf.Bytes()[:44])
				b.mutex.Unlock()
				return header, nil
			}
		case "PCM":
			if b.buf.Len() >= 1024 { // Arbitrary PCM safety buffer
				header := make([]byte, 0) // PCM has no header
				b.mutex.Unlock()
				return header, nil
			}
		}
		b.mutex.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return nil, errors.New("header timeout")
}

var closeOnce sync.Once

func (b *BufferedStdinReader) Close() error {
	var err error
	closeOnce.Do(func() {
		if b.closer != nil {
			err = b.closer.Close()
		}
		b.closed = true
	})
	return err
}
