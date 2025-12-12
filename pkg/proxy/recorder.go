package proxy

import (
	"bytes"
	"net/http"
)

// responseCapturer is a wrapper around http.ResponseWriter that copies the response body.
type responseCapturer struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (w *responseCapturer) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseCapturer) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseCapturer) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
