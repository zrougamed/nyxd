package supervisor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/runtime"
)

func dependsOnSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	base := t.TempDir()
	rt, err := runtime.New("crun", filepath.Join(base, "run", "crun"))
	if err != nil {
		t.Skip(err)
	}
	old := dependencyPoll
	dependencyPoll = 10 * time.Millisecond
	t.Cleanup(func() { dependencyPoll = old })
	return &Supervisor{rt: rt, baseDir: base, containers: map[string]*containerEntry{}, log: quiet}
}

func httpCheck(url string) *health.Config {
	c := health.Normalize(health.Config{Type: health.TypeHTTP, URL: url, Timeout: time.Second})
	return &c
}

// closedURL is an http URL nothing listens on.
func closedURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr + "/"
}

func TestComposeUpWaitsForCompletedDependency(t *testing.T) {
	s := dependsOnSupervisor(t)
	s.recordExited(ContainerSpec{ID: "p-migrate"}, 0)
	deps := map[string][]ComposeDependency{"p-app": {{ID: "p-migrate", Condition: DependsOnCompleted}}}
	if err := s.waitDependency(context.Background(), deps["p-app"][0]); err != nil {
		t.Fatalf("exit 0 dependency: %v", err)
	}

	s.recordExited(ContainerSpec{ID: "p-migrate"}, 3)
	// The failing dependency stops the stack before p-app is started (the spec
	// is not startable here, so reaching ensureComposeContainer would fail differently).
	err := s.StartComposeUpWith(context.Background(), []ContainerSpec{{ID: "p-app"}}, ComposeUpOptions{Deps: deps})
	if err == nil || !strings.Contains(err.Error(), "dependency p-migrate: exited with code 3") {
		t.Fatalf("non-zero exit: %v", err)
	}
}

func TestComposeUpCompletedDependencyStillRunningTimesOut(t *testing.T) {
	s := dependsOnSupervisor(t)
	s.containers["p-migrate"] = &containerEntry{spec: ContainerSpec{ID: "p-migrate"}}
	err := s.StartComposeUpWith(context.Background(), []ContainerSpec{{ID: "p-app"}}, ComposeUpOptions{
		Deps:        map[string][]ComposeDependency{"p-app": {{ID: "p-migrate", Condition: DependsOnCompleted}}},
		WaitTimeout: 50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "not met within 50ms (compose up --wait-timeout)") {
		t.Fatalf("got %v", err)
	}
}

func TestComposeUpWaitsForHealthyDependency(t *testing.T) {
	s := dependsOnSupervisor(t)
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-ready:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	s.containers["p-db"] = &containerEntry{spec: ContainerSpec{ID: "p-db", Healthcheck: httpCheck(srv.URL)}}

	done := make(chan error, 1)
	go func() {
		done <- s.waitDependency(context.Background(), ComposeDependency{ID: "p-db", Condition: DependsOnHealthy})
	}()
	select {
	case err := <-done:
		t.Fatalf("returned before the dependency was healthy: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(ready)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting after the dependency became healthy")
	}
}

func TestComposeUpDependencyNeverHealthy(t *testing.T) {
	s := dependsOnSupervisor(t)
	s.containers["p-db"] = &containerEntry{spec: ContainerSpec{ID: "p-db", Healthcheck: httpCheck(closedURL(t))}}
	err := s.StartComposeUpWith(context.Background(), []ContainerSpec{{ID: "p-app"}}, ComposeUpOptions{
		Deps:        map[string][]ComposeDependency{"p-app": {{ID: "p-db", Condition: DependsOnHealthy}}},
		WaitTimeout: 100 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "start p-app: dependency p-db: service_healthy: not met within") {
		t.Fatalf("got %v", err)
	}
}

func TestDependencyHealthyErrors(t *testing.T) {
	s := dependsOnSupervisor(t)
	ctx := context.Background()
	if _, err := s.dependencyHealthy(ctx, "p-gone"); err == nil || err.Error() != "not running" {
		t.Fatalf("missing: %v", err)
	}
	s.recordExited(ContainerSpec{ID: "p-crashed"}, 137)
	if _, err := s.dependencyHealthy(ctx, "p-crashed"); err == nil || !strings.Contains(err.Error(), "code 137") {
		t.Fatalf("exited: %v", err)
	}
	s.containers["p-plain"] = &containerEntry{spec: ContainerSpec{ID: "p-plain"}}
	if _, err := s.dependencyHealthy(ctx, "p-plain"); err == nil || err.Error() != "has no healthcheck" {
		t.Fatalf("no healthcheck: %v", err)
	}
	if _, err := s.dependencyCompleted(ctx, "p-gone"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("completed without record: %v", err)
	}
	// service_started needs no wait.
	if err := s.waitDependency(ctx, ComposeDependency{ID: "p-gone", Condition: "service_started"}); err != nil {
		t.Fatal(err)
	}
}

func TestDependencyHealthyUsesCheckerStatus(t *testing.T) {
	s := dependsOnSupervisor(t)
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ok.Close()
	cfg := health.Config{Type: health.TypeHTTP, URL: ok.URL, Interval: 10 * time.Millisecond, Timeout: time.Second}
	chk := health.New("p-db", cfg, "", "", quiet, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chk.Start(ctx)
	for deadline := time.Now().Add(5 * time.Second); chk.Status() != health.StatusHealthy; {
		if time.Now().After(deadline) {
			t.Fatal("checker never became healthy")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The spec's own probe would fail: a healthy checker must be enough.
	s.containers["p-db"] = &containerEntry{spec: ContainerSpec{ID: "p-db", Healthcheck: httpCheck(closedURL(t))}, checker: chk}
	healthy, err := s.dependencyHealthy(context.Background(), "p-db")
	if err != nil || !healthy {
		t.Fatalf("healthy checker: ok=%v err=%v", healthy, err)
	}
}
