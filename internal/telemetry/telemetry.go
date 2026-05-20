// Package telemetry provides lightweight internal metrics and event recording.
// No external dependency — metrics are exposed via a simple HTTP endpoint
// compatible with Prometheus text format, and events are logged via slog.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Telemetry holds all runtime counters and gauges.
type Telemetry struct {
	mu sync.RWMutex

	// Counters
	ContainersStarted  atomic.Int64
	ContainersStopped  atomic.Int64
	ContainersCrashed  atomic.Int64
	ContainersRestarted atomic.Int64
	ImagesPulled       atomic.Int64
	HealthchecksFailed atomic.Int64
	HealthchecksOK     atomic.Int64

	// Per-container gauges
	containers map[string]*ContainerMetrics
}

// ContainerMetrics holds per-container runtime data.
type ContainerMetrics struct {
	Name       string
	Status     string
	StartedAt  time.Time
	PID        int
	Restarts   int
	mu         sync.RWMutex
}

// New returns a ready Telemetry instance.
func New() *Telemetry {
	return &Telemetry{
		containers: make(map[string]*ContainerMetrics),
	}
}

// Register registers a container for metrics tracking.
func (t *Telemetry) Register(name string) *ContainerMetrics {
	t.mu.Lock()
	defer t.mu.Unlock()
	cm := &ContainerMetrics{Name: name, Status: "created"}
	t.containers[name] = cm
	return cm
}

// SetStatus updates a container's status.
func (cm *ContainerMetrics) SetStatus(status string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.Status = status
}

// Event records a structured event to slog.
func (t *Telemetry) Event(container, event string, attrs ...any) {
	slog.Info("event",
		append([]any{"container", container, "event", event}, attrs...)...,
	)
}

// ServeMetrics starts an HTTP server exposing Prometheus-compatible metrics.
// Runs until ctx is cancelled.
func (t *Telemetry) ServeMetrics(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", t.metricsHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	slog.Info("metrics server listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Warn("metrics server error", "error", err)
	}
}

func (t *Telemetry) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	// Global counters
	fmt.Fprintf(w, "# HELP nyxd_containers_started_total Total containers started\n")
	fmt.Fprintf(w, "# TYPE nyxd_containers_started_total counter\n")
	fmt.Fprintf(w, "nyxd_containers_started_total %d\n", t.ContainersStarted.Load())

	fmt.Fprintf(w, "# HELP nyxd_containers_crashed_total Total container crashes\n")
	fmt.Fprintf(w, "# TYPE nyxd_containers_crashed_total counter\n")
	fmt.Fprintf(w, "nyxd_containers_crashed_total %d\n", t.ContainersCrashed.Load())

	fmt.Fprintf(w, "# HELP nyxd_containers_restarted_total Total container restarts\n")
	fmt.Fprintf(w, "# TYPE nyxd_containers_restarted_total counter\n")
	fmt.Fprintf(w, "nyxd_containers_restarted_total %d\n", t.ContainersRestarted.Load())

	fmt.Fprintf(w, "# HELP nyxd_healthchecks_failed_total Total healthcheck failures\n")
	fmt.Fprintf(w, "# TYPE nyxd_healthchecks_failed_total counter\n")
	fmt.Fprintf(w, "nyxd_healthchecks_failed_total %d\n", t.HealthchecksFailed.Load())

	fmt.Fprintf(w, "# HELP nyxd_images_pulled_total Total images pulled from registry\n")
	fmt.Fprintf(w, "# TYPE nyxd_images_pulled_total counter\n")
	fmt.Fprintf(w, "nyxd_images_pulled_total %d\n", t.ImagesPulled.Load())

	// Per-container status gauge
	fmt.Fprintf(w, "# HELP nyxd_container_restarts Container restart count\n")
	fmt.Fprintf(w, "# TYPE nyxd_container_restarts gauge\n")
	t.mu.RLock()
	for name, cm := range t.containers {
		cm.mu.RLock()
		fmt.Fprintf(w, "nyxd_container_restarts{name=%q} %d\n", name, cm.Restarts)
		fmt.Fprintf(w, "nyxd_container_up{name=%q,status=%q} 1\n", name, cm.Status)
		cm.mu.RUnlock()
	}
	t.mu.RUnlock()
}
