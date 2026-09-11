package proxy

import (
	"bufio"
	"net"
	"net/http"
	"strings"
)

// net/http looks these up in canonical spelling while framing the response.
var transportHeaders = map[string]struct{}{
	"Content-Length":    {},
	"Content-Type":      {},
	"Transfer-Encoding": {},
	"Connection":        {},
	"Trailer":           {},
	"Date":              {},
	"Upgrade":           {},
}

// Restores the page's lowercase header names, which Go canonicalises on parse; not all clients are careful about case.
type lowercaseHeaderWriter struct {
	http.ResponseWriter
	wroteBody bool
}

// Unwrap lets http.ResponseController reach Flush and Hijack.
func (w *lowercaseHeaderWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *lowercaseHeaderWriter) WriteHeader(statusCode int) {
	// Not gated: 1xx arrives first and the final block still needs normalising.
	lowercaseKeys(w.ResponseWriter.Header())
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *lowercaseHeaderWriter) Write(b []byte) (int, error) {
	if !w.wroteBody {
		w.wroteBody = true
		lowercaseKeys(w.ResponseWriter.Header())
	}
	return w.ResponseWriter.Write(b)
}

func (w *lowercaseHeaderWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *lowercaseHeaderWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func lowercaseKeys(h http.Header) {
	for key, values := range h {
		if _, keep := transportHeaders[key]; keep {
			continue
		}
		lower := strings.ToLower(key)
		if lower == key {
			continue
		}
		delete(h, key)
		h[lower] = values
	}
}
