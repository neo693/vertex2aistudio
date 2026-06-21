package proxy

import (
	"io"
	"net/http"
)

// FlushingWriter wraps an http.ResponseWriter and calls Flush() after every write.
// This is critical for streaming SSE (Server-Sent Events) chunks without buffering.
type FlushingWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewFlushingWriter creates a new FlushingWriter.
func NewFlushingWriter(w http.ResponseWriter) *FlushingWriter {
	flusher, _ := w.(http.Flusher)
	return &FlushingWriter{
		w:       w,
		flusher: flusher,
	}
}

// Write writes bytes to the response and flushes immediately if supported.
func (fw *FlushingWriter) Write(p []byte) (n int, err error) {
	n, err = fw.w.Write(p)
	if fw.flusher != nil {
		fw.flusher.Flush()
	}
	return
}

// CopyStream proxies data from src to the flushing writer.
func CopyStream(w http.ResponseWriter, src io.Reader) (int64, error) {
	fw := NewFlushingWriter(w)
	return io.Copy(fw, src)
}
