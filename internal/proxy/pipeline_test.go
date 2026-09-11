package proxy

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func linkList(n int) string {
	var sb strings.Builder
	for i := range n {
		prefix := ""
		if i%2 == 0 {
			prefix = "Premium%20"
		}
		fmt.Fprintf(&sb, "vless://u%d@h%d.example.com:443?type=tcp#%snode-%d\n", i, i, prefix, i)
	}
	return sb.String()
}

// sameLines reports whether got is a reordering of want, line for line.
func sameLines(got, want string) bool {
	a, b := strings.Split(got, "\n"), strings.Split(want, "\n")
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// fetch returns the body, checking that Content-Length tells the truth.
func fetch(t *testing.T, client *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp := get(t, client, url, "Happ/1.2.3")
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" && cl != strconv.Itoa(len(body)) {
		t.Fatalf("Content-Length %s but %d bytes arrived", cl, len(body))
	}
	return resp, string(body)
}

// With every stage on, the body is read by the shuffler and again by the
// cache; both framings from the upstream must come out whole and consistent,
// live and when replayed after the upstream dies.
func TestPipelineAllStagesBothFramings(t *testing.T) {
	payload := linkList(60)
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			var up atomic.Bool
			up.Store(true)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if !up.Load() {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.Header().Set("subscription-userinfo", "upload=0; download=1000000000; total=10000000000; expire=0")
				w.Header().Set("announce", "Used {TRAFFIC_USED}")
				if !chunked {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
				}
				_, _ = io.WriteString(w, payload)
			}))
			defer upstream.Close()

			front := httptest.NewServer(benchProxy(&testing.B{}, upstream.URL, benchStages{rewrite: true, cache: true, shuffle: true, userAgents: true}))
			defer front.Close()

			resp, live := fetch(t, front.Client(), front.URL+"/aBcDeF123")
			if !sameLines(live, payload) {
				t.Fatal("live body is not a reordering of the upstream's")
			}
			if got := resp.Header.Get("announce"); got != "Used 1.00 GB" {
				t.Fatalf("announce = %q", got)
			}

			up.Store(false)
			_, replayed := fetch(t, front.Client(), front.URL+"/aBcDeF123")
			if !sameLines(replayed, payload) {
				t.Fatal("replayed body is not a reordering of the upstream's")
			}
		})
	}
}

// Past the buffering limit nothing may be rewritten or lost.
func TestPipelineOversizedBodyPassesThrough(t *testing.T) {
	payload := strings.Repeat("vless://u@h:1#Premium%20x\n", maxShuffleBody/26+100)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	defer upstream.Close()
	front := httptest.NewServer(benchProxy(&testing.B{}, upstream.URL, benchStages{rewrite: true, cache: true, shuffle: true}))
	defer front.Close()

	if _, body := fetch(t, front.Client(), front.URL+"/aBcDeF123"); body != payload {
		t.Fatalf("an oversized body changed: %d bytes in, %d out", len(payload), len(body))
	}
}

// An upstream that compresses anyway must be relayed untouched.
func TestPipelineCompressedBodyPassesThrough(t *testing.T) {
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = io.WriteString(zw, linkList(10))
	_ = zw.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(zipped.Bytes())
	}))
	defer upstream.Close()
	front := httptest.NewServer(benchProxy(&testing.B{}, upstream.URL, benchStages{rewrite: true, cache: true, shuffle: true, userAgents: true}))
	defer front.Close()

	req, _ := http.NewRequest(http.MethodGet, front.URL+"/aBcDeF123", nil)
	req.Header.Set("Accept-Encoding", "gzip") // stops the client from unzipping it for us
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Equal(body, zipped.Bytes()) {
		t.Fatal("a compressed body was altered")
	}
}

// Many clients at once share the buffer pool, the cache and its stored bytes;
// every one must get a whole, valid body. Run under -race.
func TestPipelineConcurrentClients(t *testing.T) {
	payload := linkList(40)
	var up atomic.Bool
	up.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer upstream.Close()
	front := httptest.NewServer(benchProxy(&testing.B{}, upstream.URL, benchStages{rewrite: true, cache: true, shuffle: true, userAgents: true}))
	defer front.Close()

	run := func() {
		var wg sync.WaitGroup
		for i := range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 10 {
					// Two short UUIDs, so cache entries are shared and not.
					_, body := fetch(t, front.Client(), fmt.Sprintf("%s/uuid%d", front.URL, i%2))
					if !sameLines(body, payload) {
						t.Error("a concurrent client got a damaged body")
						return
					}
				}
			}()
		}
		wg.Wait()
	}
	run()
	up.Store(false) // now every body is a shuffled replay of shared cached bytes
	run()
}

// A broken agent on a concurrent, cached, shuffled path gets the notice,
// and the notice never leaks into the cache for well-behaved clients.
func TestPipelineNoticeStaysOutOfTheCache(t *testing.T) {
	payload := linkList(6)
	var up atomic.Bool
	up.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer upstream.Close()
	front := httptest.NewServer(benchProxy(&testing.B{}, upstream.URL, benchStages{rewrite: true, cache: true, shuffle: true, userAgents: true}))
	defer front.Close()

	broken := get(t, front.Client(), front.URL+"/aBcDeF123", "https://sub.example.com/aBcDeF123")
	notice, _ := io.ReadAll(broken.Body)
	_ = broken.Body.Close()
	if strings.Contains(string(notice), "h0.example.com") {
		t.Fatal("the broken agent received real hosts")
	}

	_, good := fetch(t, front.Client(), front.URL+"/aBcDeF123")
	up.Store(false)
	_, replay := fetch(t, front.Client(), front.URL+"/aBcDeF123")
	if !sameLines(good, payload) || !sameLines(replay, payload) {
		t.Fatal("a well-behaved client got the notice or a damaged body")
	}
}
