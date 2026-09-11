package proxy

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
)

// readers wraps a payload in every read pattern an upstream can produce.
func readers(payload []byte) map[string]func() io.Reader {
	return map[string]func() io.Reader{
		"plain":     func() io.Reader { return bytes.NewReader(payload) },
		"one byte":  func() io.Reader { return iotest.OneByteReader(bytes.NewReader(payload)) },
		"half":      func() io.Reader { return iotest.HalfReader(bytes.NewReader(payload)) },
		"data+EOF":  func() io.Reader { return iotest.DataErrReader(bytes.NewReader(payload)) },
		"chunk 7":   func() io.Reader { return &chunkReader{r: bytes.NewReader(payload), n: 7} },
		"chunk 513": func() io.Reader { return &chunkReader{r: bytes.NewReader(payload), n: 513} },
	}
}

type chunkReader struct {
	r io.Reader
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

// readUpTo must return exactly what io.ReadAll over the same limit would,
// whatever Content-Length claims and however the bytes arrive.
func TestReadUpToMatchesReadAll(t *testing.T) {
	const maxBody = 4096
	for _, size := range []int{0, 1, 511, 512, 513, 1000, maxBody - 1, maxBody, maxBody + 1, 3 * maxBody} {
		payload := bytes.Repeat([]byte("0123456789abcdef"), size/16+1)[:size]
		want, _ := io.ReadAll(io.LimitReader(bytes.NewReader(payload), maxBody+1))
		hints := []int64{-1, 0, int64(size), int64(size) / 2, int64(size) * 2, maxBody + 10}
		for name, mk := range readers(payload) {
			for _, hint := range hints {
				got, err := readUpTo(mk(), hint, maxBody)
				if err != nil {
					t.Fatalf("size %d %s hint %d: %v", size, name, hint, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("size %d %s hint %d: got %d bytes, want %d", size, name, hint, len(got), len(want))
				}
			}
		}
	}
}

// The point of the Content-Length hint: a body of known size is read into one
// buffer, never grown.
func TestReadUpToAllocatesOnceWithContentLength(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 100_000)
	r := bytes.NewReader(payload)
	allocs := testing.AllocsPerRun(20, func() {
		r.Reset(payload)
		_, _ = readUpTo(r, int64(len(payload)), 1<<20)
	})
	// One for the buffer, one for the io.LimitedReader; a grown buffer adds more.
	if allocs > 2 {
		t.Fatalf("%.0f allocations for a body of known size, want 2", allocs)
	}
}

func TestReadUpToReportsReadErrors(t *testing.T) {
	boom := errors.New("boom")
	got, err := readUpTo(io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(boom)), -1, 1024)
	if !errors.Is(err, boom) || string(got) != "partial" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func upstreamResponse(body io.Reader, contentLength int64) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{},
		Body:          io.NopCloser(body),
		ContentLength: contentLength,
	}
}

func TestDrainBodyBuffersAndRestores(t *testing.T) {
	payload := []byte(strings.Repeat("vless://x#y\n", 100))
	for _, cl := range []int64{-1, int64(len(payload))} {
		resp := upstreamResponse(bytes.NewReader(payload), cl)
		got, ok := drainBody(resp, 1<<20)
		if !ok || !bytes.Equal(got, payload) {
			t.Fatalf("content-length %d: drained %d bytes, ok=%v", cl, len(got), ok)
		}
		// The client must still receive the whole body.
		if rest, _ := io.ReadAll(resp.Body); !bytes.Equal(rest, payload) {
			t.Fatalf("content-length %d: body left for the client is %d bytes", cl, len(rest))
		}
	}
}

// An oversized body must reach the client intact, byte for byte, even though
// part of it was already read to find out it was too big.
func TestDrainBodyOversizedStreamsIntact(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 10_000)
	for name, mk := range readers(payload) {
		for _, cl := range []int64{-1, int64(len(payload))} {
			resp := upstreamResponse(mk(), cl)
			if _, ok := drainBody(resp, 1000); ok {
				t.Fatalf("%s: a body over the limit was buffered", name)
			}
			if rest, _ := io.ReadAll(resp.Body); !bytes.Equal(rest, payload) {
				t.Fatalf("%s cl %d: client got %d of %d bytes", name, cl, len(rest), len(payload))
			}
		}
	}
}

// A second stage must get the first stage's buffer without another copy.
func TestDrainBodyReusesABufferedBody(t *testing.T) {
	resp := upstreamResponse(strings.NewReader("hello"), -1)
	first, _ := drainBody(resp, 100)
	second, ok := drainBody(resp, 100)
	if !ok || &first[0] != &second[0] {
		t.Fatal("the buffered body was copied again instead of handed over")
	}

	setBody(resp, []byte("rewritten"))
	third, ok := drainBody(resp, 100)
	if !ok || string(third) != "rewritten" {
		t.Fatalf("after setBody, drained %q", third)
	}
	if resp.Header.Get("Content-Length") != "9" || resp.ContentLength != 9 {
		t.Fatalf("framing not updated: %q %d", resp.Header.Get("Content-Length"), resp.ContentLength)
	}
}

// Reuse is only safe while nobody has read from the body yet.
func TestDrainBodyAfterAPartialReadReadsTheRest(t *testing.T) {
	resp := upstreamResponse(strings.NewReader("hello world"), -1)
	setBody(resp, []byte("hello world"))
	head := make([]byte, 6)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatal(err)
	}
	got, ok := drainBody(resp, 100)
	if !ok || string(got) != "world" {
		t.Fatalf("drained %q after reading %q, want the remainder", got, head)
	}
}

// A buffered body over a smaller stage's limit must not be handed over.
func TestDrainBodyRespectsTheLimitOnReuse(t *testing.T) {
	resp := upstreamResponse(strings.NewReader("0123456789"), -1)
	setBody(resp, []byte("0123456789"))
	if _, ok := drainBody(resp, 5); ok {
		t.Fatal("a 10-byte buffer was accepted under a 5-byte limit")
	}
	if rest, _ := io.ReadAll(resp.Body); string(rest) != "0123456789" {
		t.Fatalf("body damaged: %q", rest)
	}
}

// Without the pool the proxy still works, just allocating 32 KiB per request,
// so nothing else would notice it going missing.
func TestProxyUsesTheBufferPool(t *testing.T) {
	p := New(Options{Upstream: mustURL(t, "http://127.0.0.1:1")})
	if _, ok := p.rp.BufferPool.(*bufferPool); !ok {
		t.Fatalf("ReverseProxy.BufferPool = %T, want the shared pool", p.rp.BufferPool)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBufferPoolUnderConcurrency(t *testing.T) {
	pool := newBufferPool()
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				buf := pool.Get()
				if len(buf) != 32<<10 {
					t.Errorf("buffer of %d bytes", len(buf))
					return
				}
				buf[0], buf[len(buf)-1] = byte(i), byte(i)
				pool.Put(buf)
			}
		}()
	}
	wg.Wait()
}
