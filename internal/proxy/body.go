package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// setBody replaces a response body, keeping the framing headers true to it.
func setBody(resp *http.Response, body []byte) {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.TransferEncoding = nil
}

func writeBody(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// An oversized body is left streaming, never held in memory.
func drainBody(resp *http.Response, maxBody int64) ([]byte, bool) {
	if resp.Body == nil {
		return nil, false
	}

	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || int64(len(buf)) > maxBody {
		resp.Body = readCloser{
			Reader: io.MultiReader(bytes.NewReader(buf), resp.Body),
			Closer: resp.Body,
		}
		return nil, false
	}

	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	return buf, true
}

type readCloser struct {
	io.Reader
	io.Closer
}

// Error answers and the web page always pass through as they came.
func rewritable(resp *http.Response) bool {
	return resp.StatusCode == http.StatusOK && !isHTML(resp.Header)
}

func isHTML(h http.Header) bool {
	return strings.Contains(strings.ToLower(h.Get("Content-Type")), "text/html")
}

func isCompressed(h http.Header) bool {
	enc := strings.ToLower(strings.TrimSpace(h.Get("Content-Encoding")))
	return enc != "" && enc != "identity"
}
