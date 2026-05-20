package compose_test

import (
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/health"
)

func TestHealthcheckToConfig_CMD(t *testing.T) {
	cfg, err := compose.HealthcheckToConfig(&compose.Healthcheck{
		Test:     []string{"CMD", "/bin/true"},
		Interval: compose.Duration{Duration: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("nil cfg")
	}
	if cfg.Type != health.TypeExec {
		t.Fatalf("type: %s", cfg.Type)
	}
	if len(cfg.Command) != 1 || cfg.Command[0] != "/bin/true" {
		t.Fatalf("command: %#v", cfg.Command)
	}
}

func TestHealthcheckToConfig_None(t *testing.T) {
	cfg, err := compose.HealthcheckToConfig(&compose.Healthcheck{Test: []string{"NONE"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != health.TypeNone {
		t.Fatalf("got %s", cfg.Type)
	}
}
