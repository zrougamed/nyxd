// Package health implements container healthchecks.
// Supports: exec (runs inside container via crun exec), http, tcp.
package health

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// Type identifies the healthcheck kind.
type Type string

const (
	TypeExec Type = "exec"
	TypeHTTP Type = "http"
	TypeTCP  Type = "tcp"
	TypeNone Type = "none"
)

// Config defines how to check container health.
type Config struct {
	Type        Type          `json:"type,omitempty"`
	Command     []string      `json:"command,omitempty"`
	URL         string        `json:"url,omitempty"`
	Address     string        `json:"address,omitempty"`
	Interval    time.Duration `json:"interval,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
	Retries     int           `json:"retries,omitempty"`
	StartPeriod time.Duration `json:"startPeriod,omitempty"`
}

// Status of a container's healthcheck.
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
	StatusStarting  Status = "starting"
)

// Checker runs healthchecks for a container and notifies on status change.
type Checker struct {
	containerID string
	cfg         Config
	crunBin     string // path to crun binary for exec checks
	crunRoot    string
	log         *slog.Logger

	mu          sync.RWMutex
	status      Status
	consecutive int // consecutive failures
	onUnhealthy func(containerID string) // called when container becomes unhealthy

	cancel context.CancelFunc
}

// New creates a Checker. onUnhealthy is called (once) when a container fails its healthcheck.
func New(containerID string, cfg Config, crunBin, crunRoot string, log *slog.Logger, onUnhealthy func(string)) *Checker {
	cfg = Normalize(cfg)
	return &Checker{
		containerID: containerID,
		cfg:         cfg,
		crunBin:     crunBin,
		crunRoot:    crunRoot,
		log:         log,
		status:      StatusStarting,
		onUnhealthy: onUnhealthy,
	}
}

// Normalize applies the same defaults as [New] (interval, timeout, retries).
func Normalize(cfg Config) Config {
	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	return cfg
}

// Start begins running health checks in the background.
// Call Stop to clean up.
func (c *Checker) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go c.loop(ctx)
}

// Stop halts the health checker.
func (c *Checker) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// Status returns the current health status.
func (c *Checker) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// loop runs healthchecks on the configured interval.
func (c *Checker) loop(ctx context.Context) {
	// Wait out start period before first check.
	if c.cfg.StartPeriod > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.cfg.StartPeriod):
		}
	}

	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	// Run once immediately after start period.
	c.check(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.check(ctx)
		}
	}
}

func (c *Checker) check(ctx context.Context) {
	if c.cfg.Type == TypeNone || c.cfg.Type == "" {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	err := Probe(checkCtx, c.cfg, c.containerID, c.crunBin, c.crunRoot)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err != nil {
		c.consecutive++
		c.log.Warn("healthcheck failed",
			"id", c.containerID,
			"type", c.cfg.Type,
			"err", err,
			"consecutive", c.consecutive,
		)
		if c.consecutive >= c.cfg.Retries && c.status != StatusUnhealthy {
			c.status = StatusUnhealthy
			if c.onUnhealthy != nil {
				go c.onUnhealthy(c.containerID)
			}
		}
	} else {
		if c.consecutive > 0 {
			c.log.Info("healthcheck recovered", "id", c.containerID)
		}
		c.consecutive = 0
		c.status = StatusHealthy
	}
}

// Probe runs a single health probe (same semantics as one Checker evaluation).
func Probe(ctx context.Context, cfg Config, containerID, crunBin, crunRoot string) error {
	switch cfg.Type {
	case TypeNone, "":
		return nil
	case TypeExec:
		return probeExec(ctx, cfg, containerID, crunBin, crunRoot)
	case TypeHTTP:
		return probeHTTP(ctx, cfg)
	case TypeTCP:
		return probeTCP(ctx, cfg)
	default:
		return fmt.Errorf("unknown healthcheck type %q", cfg.Type)
	}
}

func probeExec(ctx context.Context, cfg Config, containerID, crunBin, crunRoot string) error {
	if len(cfg.Command) == 0 {
		return fmt.Errorf("exec healthcheck: no command")
	}
	args := []string{"--root", crunRoot, "exec", containerID}
	args = append(args, cfg.Command...)
	cmd := exec.CommandContext(ctx, crunBin, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

func probeHTTP(ctx context.Context, cfg Config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http healthcheck: %d", resp.StatusCode)
	}
	return nil
}

func probeTCP(ctx context.Context, cfg Config) error {
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// WaitReady repeatedly probes until success, ctx is cancelled, or the deadline from ctx expires.
// Honors cfg.StartPeriod (sleep before first probe). Uses cfg.Timeout per attempt.
func WaitReady(ctx context.Context, log *slog.Logger, cfg Config, containerID, crunBin, crunRoot string) error {
	if cfg.Type == TypeNone || cfg.Type == "" {
		return nil
	}
	if cfg.StartPeriod > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cfg.StartPeriod):
		}
	}
	const tick = 500 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		err := Probe(pctx, cfg, containerID, crunBin, crunRoot)
		cancel()
		if err == nil {
			return nil
		}
		if log != nil {
			log.Debug("readiness probe failed", "id", containerID, "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(tick):
		}
	}
}
