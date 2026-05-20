package compose_test

import (
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/compose"
)

const validCompose = `
version: "1"
networks:
  mynet:
    driver: bridge
services:
  app:
    image: nginx:latest
    networks:
      - mynet
    restart: always
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost"]
      interval: 30s
      timeout: 5s
      retries: 3
`

func TestParseValid(t *testing.T) {
	stack, err := compose.Parse([]byte(validCompose))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stack.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(stack.Services))
	}
	svc := stack.Services["app"]
	if svc.Image != "nginx:latest" {
		t.Errorf("expected nginx:latest, got %s", svc.Image)
	}
	if svc.Restart != compose.RestartAlways {
		t.Errorf("expected restart=always, got %s", svc.Restart)
	}
	if svc.Healthcheck == nil {
		t.Fatal("expected healthcheck to be set")
	}
	if svc.Healthcheck.Interval.Duration != 30*time.Second {
		t.Errorf("expected 30s interval, got %v", svc.Healthcheck.Interval.Duration)
	}
}

func TestParseMissingImage(t *testing.T) {
	bad := `
version: "1"
services:
  app:
    restart: always
`
	_, err := compose.Parse([]byte(bad))
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestParseDependencyCycle(t *testing.T) {
	cyclic := `
version: "1"
services:
  a:
    image: img:1
    depends_on: [b]
  b:
    image: img:2
    depends_on: [a]
`
	_, err := compose.Parse([]byte(cyclic))
	if err == nil {
		t.Fatal("expected error for dependency cycle")
	}
}

func TestParseUnknownDependency(t *testing.T) {
	bad := `
version: "1"
services:
  a:
    image: img:1
    depends_on: [ghost]
`
	_, err := compose.Parse([]byte(bad))
	if err == nil {
		t.Fatal("expected error for unknown dependency")
	}
}

func TestParseDependsOnMapForm(t *testing.T) {
	y := `
version: "1"
services:
  subscriber:
    image: python:3.12-slim
    depends_on:
      rabbitmq:
        condition: service_healthy
  rabbitmq:
    image: rabbitmq:3-management
`
	stack, err := compose.Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sub := stack.Services["subscriber"]
	if len(sub.DependsOn) != 1 || sub.DependsOn[0] != "rabbitmq" {
		t.Fatalf("depends_on: got %#v", []string(sub.DependsOn))
	}
}

func TestTopologicalOrder(t *testing.T) {
	stack, err := compose.Parse([]byte(`
version: "1"
services:
  c:
    image: img:c
    depends_on: [b]
  b:
    image: img:b
    depends_on: [a]
  a:
    image: img:a
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	order := compose.TopologicalOrder(stack)
	// a must come before b which must come before c
	pos := func(name string) int {
		for i, v := range order {
			if v == name {
				return i
			}
		}
		return -1
	}
	if pos("a") >= pos("b") || pos("b") >= pos("c") {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestRestartPolicyValidation(t *testing.T) {
	bad := `
version: "1"
services:
  app:
    image: img:1
    restart: invalid-policy
`
	_, err := compose.Parse([]byte(bad))
	if err == nil {
		t.Fatal("expected error for invalid restart policy")
	}
}

func TestDefaultNoNewPrivileges(t *testing.T) {
	stack, err := compose.Parse([]byte(validCompose))
	if err != nil {
		t.Fatal(err)
	}
	svc := stack.Services["app"]
	if svc.NoNewPrivileges == nil || !*svc.NoNewPrivileges {
		t.Error("expected no_new_privileges=true by default")
	}
}
