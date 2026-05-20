package compose

import (
	"fmt"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/health"
)

// HealthcheckToConfig maps a compose-spec healthcheck block to the internal checker config.
// Returns nil when there is nothing to probe (nil input, NONE, or empty test).
func HealthcheckToConfig(h *Healthcheck) (*health.Config, error) {
	if h == nil {
		return nil, nil
	}
	if len(h.Test) == 0 {
		return nil, nil
	}
	kind := strings.ToUpper(strings.TrimSpace(h.Test[0]))
	cfg := &health.Config{
		Interval:    h.Interval.Duration,
		Timeout:     h.Timeout.Duration,
		Retries:     h.Retries,
		StartPeriod: h.StartPeriod.Duration,
	}
	switch kind {
	case "NONE":
		cfg.Type = health.TypeNone
		return cfg, nil
	case "CMD":
		cfg.Type = health.TypeExec
		cfg.Command = append([]string(nil), h.Test[1:]...)
	case "CMD-SHELL":
		cfg.Type = health.TypeExec
		cfg.Command = []string{"/bin/sh", "-c", strings.Join(h.Test[1:], " ")}
	default:
		// Treat unknown first token as argv (lenient for hand-written files).
		cfg.Type = health.TypeExec
		cfg.Command = append([]string(nil), h.Test...)
	}
	if len(cfg.Command) == 0 && cfg.Type == health.TypeExec {
		return nil, fmt.Errorf("healthcheck: empty command")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Retries <= 0 {
		cfg.Retries = 3
	}
	return cfg, nil
}
