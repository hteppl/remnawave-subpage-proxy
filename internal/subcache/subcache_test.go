package subcache

import (
	"net/http"
	"testing"
	"time"
)

func entry(body string) *Entry {
	return &Entry{
		Status: http.StatusOK,
		Header: http.Header{"Announce": []string{"hi"}},
		Body:   []byte(body),
	}
}

func TestStoreAndServe(t *testing.T) {
	c := New(time.Hour, 1<<20, 1<<16)
	key := Key("abc", "", "Happ/1.0", "gzip")

	if _, ok := c.Get(key); ok {
		t.Fatal("empty cache should miss")
	}

	c.Put(key, entry("vless://one"))

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("expected a hit")
	}
	if string(got.Body) != "vless://one" {
		t.Errorf("body = %q", got.Body)
	}
	if got.Status != http.StatusOK {
		t.Errorf("status = %d", got.Status)
	}
}

func TestKeySeparatesVariants(t *testing.T) {
	c := New(time.Hour, 1<<20, 1<<16)

	c.Put(Key("abc", "clash", "Happ/1.0", ""), entry("clash config"))
	c.Put(Key("abc", "json", "Happ/1.0", ""), entry("json config"))
	c.Put(Key("abc", "", "v2rayNG", ""), entry("base64 links"))
	c.Put(Key("abc", "", "v2rayNG", "gzip"), entry("gzipped bytes"))

	// A gzipped body replayed to a client that never asked for gzip would be
	// unreadable, so Accept-Encoding has to be part of the identity.
	for _, tc := range []struct{ key, want string }{
		{Key("abc", "clash", "Happ/1.0", ""), "clash config"},
		{Key("abc", "json", "Happ/1.0", ""), "json config"},
		{Key("abc", "", "v2rayNG", ""), "base64 links"},
		{Key("abc", "", "v2rayNG", "gzip"), "gzipped bytes"},
	} {
		got, ok := c.Get(tc.key)
		if !ok || string(got.Body) != tc.want {
			t.Errorf("got %q (ok=%v), want %q", got.Body, ok, tc.want)
		}
	}

	if _, ok := c.Get(Key("other", "", "v2rayNG", "")); ok {
		t.Error("a different short UUID must not hit")
	}
}

func TestExpiry(t *testing.T) {
	c := New(time.Minute, 1<<20, 1<<16)
	now := time.Now()
	c.now = func() time.Time { return now }

	key := Key("abc", "", "ua", "")
	c.Put(key, entry("payload"))

	now = now.Add(59 * time.Second)
	if _, ok := c.Get(key); !ok {
		t.Error("entry should still be usable inside the TTL")
	}

	now = now.Add(2 * time.Second)
	if _, ok := c.Get(key); ok {
		t.Error("entry should be gone after the TTL")
	}
	if n, _ := c.Stats(); n != 0 {
		t.Errorf("expired entry should be dropped on read, %d left", n)
	}
}

func TestOversizedBodyIsNotStored(t *testing.T) {
	c := New(time.Hour, 1<<20, 16)
	key := Key("abc", "", "ua", "")

	c.Put(key, entry("this body is definitely longer than sixteen bytes"))

	if _, ok := c.Get(key); ok {
		t.Error("a body over MaxBody must not be stored")
	}
}

func TestByteBudgetEvictsOldest(t *testing.T) {
	c := New(time.Hour, 300, 1<<16)
	now := time.Now()
	c.now = func() time.Time { return now }

	body := string(make([]byte, 100))
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		now = now.Add(time.Duration(i) * time.Second)
		c.Put(Key(name, "", "ua", ""), entry(body))
	}

	_, bytes := c.Stats()
	if bytes > 300 {
		t.Errorf("cache holds %d bytes, want at most 300", bytes)
	}
	if _, ok := c.Get(Key("a", "", "ua", "")); ok {
		t.Error("the oldest entry should have been evicted first")
	}
	if _, ok := c.Get(Key("e", "", "ua", "")); !ok {
		t.Error("the newest entry should have survived")
	}
}

func TestPutReplacesAndAccountsBytes(t *testing.T) {
	c := New(time.Hour, 1<<20, 1<<16)
	key := Key("abc", "", "ua", "")

	c.Put(key, entry("first"))
	_, afterFirst := c.Stats()
	c.Put(key, entry("first"))
	n, afterSecond := c.Stats()

	if n != 1 {
		t.Errorf("entries = %d, want 1", n)
	}
	if afterFirst != afterSecond {
		t.Errorf("byte accounting drifted on replace: %d then %d", afterFirst, afterSecond)
	}
}
