package proxy

import (
	"bufio"
	"net"
	"net/http"
	"strings"
)

// transportHeaders keep canonical spelling: net/http looks them up that way
// while framing the response.
var transportHeaders = map[string]struct{}{
	"Content-Length":    {},
	"Content-Type":      {},
	"Transfer-Encoding": {},
	"Connection":        {},
	"Trailer":           {},
	"Date":              {},
	"Upgrade":           {},
}

// lowercaseHeaderWriter restores the lowercase spelling the subscription page
// uses, which Go canonicalises away on parse. Legal either way, but this proxy
// is a drop-in and subscription clients are not uniformly careful.
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
