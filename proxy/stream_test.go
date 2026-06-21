package proxy

import (
	"bytes"
	"net/http"
	"testing"
)

type mockResponseWriter struct {
	buf          bytes.Buffer
	flushedCount int
}

func (m *mockResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (m *mockResponseWriter) Write(p []byte) (int, error) {
	return m.buf.Write(p)
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {}

func (m *mockResponseWriter) Flush() {
	m.flushedCount++
}

func TestFlushingWriter(t *testing.T) {
	mockW := &mockResponseWriter{}
	fw := NewFlushingWriter(mockW)

	data := []byte("hello")
	n, err := fw.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected to write %d bytes, wrote %d", len(data), n)
	}

	if mockW.buf.String() != "hello" {
		t.Errorf("expected buffer to contain 'hello', got %q", mockW.buf.String())
	}

	if mockW.flushedCount != 1 {
		t.Errorf("expected Flush() to be called 1 time, got %d", mockW.flushedCount)
	}
}
