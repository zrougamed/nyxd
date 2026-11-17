package compose_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/compose"
	"github.com/zrougamed/nyxd/internal/supervisor"
)

const dependsOnStack = `
services:
  db:
    image: postgres:17
    healthcheck:
      test: ["CMD", "pg_isready"]
  migrate:
    image: app
    restart: "no"
    depends_on:
      db: {condition: service_healthy}
  cache:
    image: redis
  app:
    image: app
    depends_on:
      db: {condition: service_healthy}
      migrate: {condition: service_completed_successfully}
      cache: {condition: service_started, restart: true}
`

func TestDependsOnConditions(t *testing.T) {
	st, err := compose.Parse([]byte(dependsOnStack))
	if err != nil {
		t.Fatal(err)
	}
	want := compose.DependsOn{
		{Service: "cache", Condition: compose.ConditionStarted, Restart: true},
		{Service: "db", Condition: compose.ConditionHealthy},
		{Service: "migrate", Condition: compose.ConditionCompleted},
	}
	if got := st.Services["app"].DependsOn; !reflect.DeepEqual(got, want) {
		t.Fatalf("app depends_on:\n got %+v\nwant %+v", got, want)
	}
	if got := st.Services["app"].DependsOn.Names(); !reflect.DeepEqual(got, []string{"cache", "db", "migrate"}) {
		t.Fatalf("names: %v", got)
	}

	waits := compose.DependencyWaits(st, "p")
	wantWaits := map[string][]supervisor.ComposeDependency{
		"p-app": {
			{ID: "p-db", Condition: supervisor.DependsOnHealthy},
			{ID: "p-migrate", Condition: supervisor.DependsOnCompleted},
		},
		"p-migrate": {{ID: "p-db", Condition: supervisor.DependsOnHealthy}},
	}
	if !reflect.DeepEqual(waits, wantWaits) {
		t.Fatalf("waits:\n got %+v\nwant %+v", waits, wantWaits)
	}

	order := compose.TopologicalOrder(st)
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["db"] > pos["migrate"] || pos["migrate"] > pos["app"] || pos["cache"] > pos["app"] {
		t.Fatalf("order %v", order)
	}

	ws := compose.DependsOnWarnings(st)
	if len(ws) != 1 || !strings.Contains(ws[0], `"cache" restart: true is not supported`) {
		t.Fatalf("warnings: %q", ws)
	}
}

func TestDependsOnHealthyNeedsHealthcheck(t *testing.T) {
	for name, hc := range map[string]string{
		"missing": "",
		"NONE":    "\n    healthcheck:\n      test: [\"NONE\"]",
	} {
		y := "services:\n  db:\n    image: postgres" + hc + `
  app:
    image: app
    depends_on:
      db: {condition: service_healthy}
`
		_, err := compose.Parse([]byte(y))
		if err == nil || !strings.Contains(err.Error(), `"db" has no healthcheck`) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestDependsOnRejectsUnknownCondition(t *testing.T) {
	_, err := compose.Parse([]byte(`
services:
  db: {image: postgres}
  app:
    image: app
    depends_on:
      db: {condition: service_ready}
`))
	if err == nil || !strings.Contains(err.Error(), `unknown condition "service_ready"`) {
		t.Fatalf("got %v", err)
	}
}

func TestDependsOnCompletedWithRestartingDependencyWarns(t *testing.T) {
	st, err := compose.Parse([]byte(`
services:
  job:
    image: app
    restart: always
  app:
    image: app
    depends_on:
      job: {condition: service_completed_successfully}
`))
	if err != nil {
		t.Fatal(err)
	}
	ws := compose.DependsOnWarnings(st)
	if len(ws) != 1 || !strings.Contains(ws[0], "never completes") {
		t.Fatalf("warnings: %q", ws)
	}
	if compose.DependsOnWarnings(nil) != nil {
		t.Fatal("nil stack")
	}
}
