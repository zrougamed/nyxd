package health_test

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/health"
)

func TestWaitReady_TCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.Close()
		accepted <- struct{}{}
	}()

	addr := ln.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg := health.Config{
		Type:    health.TypeTCP,
		Address: addr,
		Timeout: time.Second,
	}
	if err := health.WaitReady(ctx, slog.Default(), cfg, "noop", "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("accept did not run")
	}
}
