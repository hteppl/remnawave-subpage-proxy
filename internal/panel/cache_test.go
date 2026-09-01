package panel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type countingFetcher struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
	info    *Info
	err     error
}

func (f *countingFetcher) SubscriptionInfo(context.Context, string, string) (*Info, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.release != nil {
		<-f.release
	}
	return f.info, f.err
}

func (f *countingFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestCacheServesRepeatLookups(t *testing.T) {
	fetcher := &countingFetcher{info: &Info{IsFound: true}}
	cache := NewCache(fetcher, time.Minute, time.Minute, 100)

	for range 5 {
		if _, err := cache.SubscriptionInfo(context.Background(), "abc", ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := fetcher.callCount(); got != 1 {
		t.Errorf("fetcher called %d times, want 1", got)
	}
}

func TestCacheExpires(t *testing.T) {
	fetcher := &countingFetcher{info: &Info{IsFound: true}}
	cache := NewCache(fetcher, time.Minute, time.Minute, 100)

	now := time.Now()
	cache.now = func() time.Time { return now }

	if _, err := cache.SubscriptionInfo(context.Background(), "abc", ""); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := cache.SubscriptionInfo(context.Background(), "abc", ""); err != nil {
		t.Fatal(err)
	}
	if got := fetcher.callCount(); got != 2 {
		t.Errorf("fetcher called %d times, want 2 after expiry", got)
	}
}

func TestCacheNegativeOnlyForNotFound(t *testing.T) {
	t.Run("not found is cached", func(t *testing.T) {
		fetcher := &countingFetcher{err: ErrNotFound}
		cache := NewCache(fetcher, time.Minute, time.Minute, 100)

		for range 3 {
			if _, err := cache.SubscriptionInfo(context.Background(), "abc", ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v", err)
			}
		}
		if got := fetcher.callCount(); got != 1 {
			t.Errorf("fetcher called %d times, want 1", got)
		}
	})

	t.Run("transient failures are retried", func(t *testing.T) {
		fetcher := &countingFetcher{err: errors.New("panel timeout")}
		cache := NewCache(fetcher, time.Minute, time.Minute, 100)

		for range 3 {
			if _, err := cache.SubscriptionInfo(context.Background(), "abc", ""); err == nil {
				t.Fatal("expected an error")
			}
		}
		if got := fetcher.callCount(); got != 3 {
			t.Errorf("fetcher called %d times, want 3: a timeout must not be cached", got)
		}
	})
}

func TestCacheCollapsesConcurrentLookups(t *testing.T) {
	fetcher := &countingFetcher{info: &Info{IsFound: true}, release: make(chan struct{})}
	cache := NewCache(fetcher, time.Minute, time.Minute, 100)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = cache.SubscriptionInfo(context.Background(), "abc", "")
		}()
	}

	// Let every goroutine reach the in-flight check before the fetch completes.
	time.Sleep(50 * time.Millisecond)
	close(fetcher.release)
	wg.Wait()

	if got := fetcher.callCount(); got != 1 {
		t.Errorf("fetcher called %d times, want 1: concurrent lookups must collapse", got)
	}
}

func TestCacheBoundsSize(t *testing.T) {
	fetcher := &countingFetcher{info: &Info{IsFound: true}}
	cache := NewCache(fetcher, time.Minute, time.Minute, 10)

	for i := range 100 {
		if _, err := cache.SubscriptionInfo(context.Background(), string(rune('a'+i%26))+string(rune('0'+i/26)), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := cache.Len(); got > 10 {
		t.Errorf("cache holds %d entries, want at most 10", got)
	}
}

func TestUserByteAccessors(t *testing.T) {
	u := User{
		TrafficUsedBytes:  "10500000000",
		TrafficLimitBytes: "",
		ExpiresAt:         "2026-12-31T23:59:59.000Z",
	}
	if got := u.UsedBytes(); got != 10_500_000_000 {
		t.Errorf("UsedBytes = %d", got)
	}
	if got := u.LimitBytes(); got != -1 {
		t.Errorf("LimitBytes on an empty string = %d, want -1", got)
	}
	if _, ok := u.Expiry(); !ok {
		t.Error("Expiry should parse an RFC3339 timestamp")
	}
}
