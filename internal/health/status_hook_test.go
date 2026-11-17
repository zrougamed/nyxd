package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnStatusChange(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	c := New("c1", Config{Type: TypeHTTP, URL: srv.URL, Interval: 10 * time.Millisecond, Timeout: time.Second, Retries: 1},
		"", "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	var mu sync.Mutex
	var got []Status
	c.OnStatusChange(func(s Status) { mu.Lock(); got = append(got, s); mu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	wait := func(want Status) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			mu.Lock()
			n := len(got)
			last := Status("")
			if n > 0 {
				last = got[n-1]
			}
			mu.Unlock()
			if last == want {
				return
			}
		}
		t.Fatalf("never reported %s (got %v)", want, got)
	}
	wait(StatusHealthy)
	fail.Store(true)
	wait(StatusUnhealthy)
	c.Stop()
	mu.Lock()
	defer mu.Unlock()
	// Each change is reported once, not once per check.
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Fatalf("repeated status: %v", got)
		}
	}
}
