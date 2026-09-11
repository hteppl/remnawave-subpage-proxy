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
	resp.Body = newBufferedBody(body)
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.TransferEncoding = nil
}

func writeBody(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// An oversized body is left streaming, never held in memory. A body another
// stage already buffered is handed over as is, not read and copied again.
func drainBody(resp *http.Response, maxBody int64) ([]byte, bool) {
	if resp.Body == nil {
		return nil, false
	}
	if b, ok := resp.Body.(*bufferedBody); ok && b.Len() == len(b.buf) {
		return b.buf, int64(len(b.buf)) <= maxBody
	}

	buf, err := readUpTo(resp.Body, resp.ContentLength, maxBody)
	if err != nil || int64(len(buf)) > maxBody {
		resp.Body = readCloser{
			Reader: io.MultiReader(bytes.NewReader(buf), resp.Body),
			Closer: resp.Body,
		}
		return nil, false
	}

	_ = resp.Body.Close()
	resp.Body = newBufferedBody(buf)
	return buf, true
}

// readUpTo reads at most maxBody+1 bytes, sized from Content-Length when the
// upstream sent one; io.ReadAll would start at 512 bytes and double from there.
func readUpTo(r io.Reader, contentLength, maxBody int64) ([]byte, error) {
	size := int64(512)
	if contentLength > 0 && contentLength <= maxBody {
		size = contentLength + 1
	}
	buf := make([]byte, 0, size)
	r = io.LimitReader(r, maxBody+1)
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
}

// bufferedBody is a response body already held in memory.
type bufferedBody struct {
	*bytes.Reader
	buf []byte
}

func newBufferedBody(buf []byte) *bufferedBody {
	return &bufferedBody{Reader: bytes.NewReader(buf), buf: buf}
}

func (*bufferedBody) Close() error { return nil }

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
