package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestSystemDF(t *testing.T) {
	body := `{"categories":[{"type":"images","count":2,"active":1,"size":3630000,"reclaimable":1054},
{"type":"build_cache","count":1,"size":4096,"reclaimable":4096},
{"type":"containers","count":2,"active":1,"size":24576,"reclaimable":0},
{"type":"logs","count":1,"active":1,"size":0,"reclaimable":0},
{"type":"volumes","count":1,"active":0,"size":8192,"reclaimable":8192}],
"items":{"images":[{"name":"demo:1","size":3630000,"unique":1054,"active":false},{"name":"alpine:3.20","size":3629000,"unique":612,"active":true}],
"containers":[{"name":"web","size":24576,"active":true,"detail":"running"}],
"logs":[{"name":"web","size":0,"active":true}],
"volumes":[{"name":"proj/data","size":8192,"active":false,"detail":"/var/lib/nyxd/volumes/proj/data"}]}}`
	sock := stub(t, routes(map[string]http.HandlerFunc{"/v1/system/df": reply(200, body)}))
	out, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "system", "df"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TYPE          COUNT   ACTIVE   SIZE", "Images        2       1", "Build cache   1       -", "Volumes       1       0        8.00KiB   8.00KiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	out, _, _ = capture(t, "", func() error { return run([]string{"-socket", sock, "system", "df", "-v"}) })
	for _, want := range []string{"Images:", "alpine:3.20", "demo:1", "Containers:", "running", "Volumes:", "/var/lib/nyxd/volumes/proj/data"} {
		if !strings.Contains(out, want) {
			t.Errorf("-v missing %q", want)
		}
	}
	if strings.Index(out, "alpine:3.20") > strings.Index(out, "demo:1") {
		t.Error("items not sorted")
	}
	out, _, _ = capture(t, "", func() error { return run([]string{"-socket", sock, "system", "df", "--json"}) })
	if out != body {
		t.Error("--json passes the answer through")
	}
	if err := run([]string{"-socket", sock, "system", "df", "-x"}); err == nil {
		t.Error("bad flag")
	}
	old := stub(t, reply(404, "404 page not found"))
	if err := run([]string{"-socket", old, "system", "df"}); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("old daemon: %v", err)
	}
}

func TestSystemPrune(t *testing.T) {
	var got string
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		got = string(b[:n])
		dry := strings.Contains(got, `"dry_run":true`)
		_, _ = w.Write([]byte(`{"ok":true,"dry_run":` + map[bool]string{true: "true", false: "false"}[dry] +
			`,"removed":{"containers":["job"],"build_cache":["k1","k2"],"layers":["3 unreferenced"]},"reclaimed":20480}`))
	})
	out, _, err := capture(t, "", func() error {
		return run([]string{"-socket", sock, "system", "prune", "--images", "--volumes"})
	})
	if err != nil || !strings.Contains(got, `"images":true`) || !strings.Contains(got, `"volumes":true`) {
		t.Fatalf("request %q: %v", got, err)
	}
	for _, want := range []string{"Removed 1 containers\n  job\n", "Removed 2 build cache\n", "Reclaimed 20.0KiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	out, _, _ = capture(t, "", func() error { return run([]string{"-socket", sock, "system", "prune", "--dry-run"}) })
	if !strings.Contains(out, "Would remove 1 containers") || !strings.Contains(out, "Would reclaim about 20.0KiB") {
		t.Errorf("dry run:\n%s", out)
	}
	for _, args := range [][]string{{"system"}, {"system", "bogus"}, {"system", "prune", "--force"}} {
		if err := run(append([]string{"-socket", sock}, args...)); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if err := run([]string{"-socket", sock, "system", "--help"}); err != nil {
		t.Error(err)
	}
	broken := stub(t, reply(500, "boom"))
	if err := run([]string{"-socket", broken, "system", "prune"}); err == nil {
		t.Error("500 accepted")
	}
}
