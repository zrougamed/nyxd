package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestUpdateCommand(t *testing.T) {
	var method string
	body := `{"containers":[
{"id":"web","image":"app:1","current":"sha256:aaa","available":"sha256:bbbbbbbbbbbbbbbbbbbb","action":"update available"},
{"id":"db","image":"postgres:17","action":"up to date"},
{"id":"bad","image":"x:1","action":"rolled back","available":"sha256:cccccccccccccccc","error":"readiness: unhealthy"}]}`
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		_, _ = w.Write([]byte(body))
	})
	out, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "update", "--check"}) })
	if err != nil || method != http.MethodGet {
		t.Fatalf("check: %v %s", err, method)
	}
	for _, want := range []string{"CONTAINER", "web", "update available (bbbbbbbbbbbb)", "db", "up to date", "rolled back (cccccccccccc): readiness: unhealthy"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if _, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "update"}) }); err != nil || method != http.MethodPost {
		t.Fatalf("update: %v %s", err, method)
	}
	out, _, _ = capture(t, "", func() error { return run([]string{"-socket", sock, "update", "--json"}) })
	if out != body {
		t.Error("--json")
	}
	none := stub(t, reply(200, `{"containers":[]}`))
	out, _, _ = capture(t, "", func() error { return run([]string{"-socket", none, "update"}) })
	if !strings.Contains(out, "no running container is labelled") {
		t.Errorf("none: %q", out)
	}
	failing := stub(t, reply(200, `{"containers":[{"id":"w","image":"a:1","action":"error","error":"registry down"}]}`))
	if _, _, err := capture(t, "", func() error { return run([]string{"-socket", failing, "update"}) }); err == nil {
		t.Error("error result not reported")
	}
	for _, args := range [][]string{{"update", "--now"}} {
		if err := run(append([]string{"-socket", sock}, args...)); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if err := run([]string{"-socket", sock, "update", "--help"}); err != nil {
		t.Error(err)
	}
	old := stub(t, reply(404, "404 page not found"))
	if err := run([]string{"-socket", old, "update"}); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("old: %v", err)
	}
	broken := stub(t, reply(503, "updates not available"))
	if err := run([]string{"-socket", broken, "update"}); err == nil {
		t.Error("503 accepted")
	}
}

func TestRunLabels(t *testing.T) {
	o, err := parseRunArgs([]string{"--label", "nyx.update=digest", "-l=team=web", "alpine"})
	if err != nil || o.labels["nyx.update"] != "digest" || o.labels["team"] != "web" {
		t.Fatalf("%+v %v", o.labels, err)
	}
	if _, err := parseRunArgs([]string{"--label", "=x", "alpine"}); err == nil {
		t.Error("empty label key accepted")
	}
}
