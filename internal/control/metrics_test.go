package control

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/cgstats"
	"github.com/zrougamed/nyxd/internal/supervisor"
)

var (
	sampleRE = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{([a-zA-Z_][a-zA-Z0-9_]*="(\\.|[^"\\])*"(,[a-zA-Z_][a-zA-Z0-9_]*="(\\.|[^"\\])*")*)\})? (\S+)$`)
	typeRE   = regexp.MustCompile(`^# TYPE ([a-zA-Z_:][a-zA-Z0-9_:]*) (counter|gauge)$`)
)

// checkExposition applies the text format rules promtool checks that matter
// here: every sample belongs to the family whose TYPE came last, families
// appear once, counters end in _total, values parse.
func checkExposition(t *testing.T, text string) map[string][]string {
	t.Helper()
	families := map[string][]string{}
	seen := map[string]bool{}
	cur, curType := "", ""
	for i, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
		case strings.HasPrefix(line, "# TYPE "):
			m := typeRE.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("line %d: bad TYPE %q", i+1, line)
			}
			if seen[m[1]] {
				t.Fatalf("line %d: family %s repeated", i+1, m[1])
			}
			seen[m[1]] = true
			cur, curType = m[1], m[2]
			if curType == "counter" && !strings.HasSuffix(cur, "_total") {
				t.Errorf("counter %s does not end in _total", cur)
			}
		default:
			m := sampleRE.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("line %d: bad sample %q", i+1, line)
			}
			if m[1] != cur {
				t.Fatalf("line %d: sample %s outside its family (%s)", i+1, m[1], cur)
			}
			if m[len(m)-1] == "NaN" {
				t.Errorf("line %d: NaN", i+1)
			}
			families[cur] = append(families[cur], line)
		}
	}
	return families
}

func TestWriteMetrics(t *testing.T) {
	var b strings.Builder
	writeMetrics(&b, metricsInput{
		version: "v1.3.0", commit: "abc", uptime: 90 * time.Second, exited: 2, images: 7,
		containers: []supervisor.ContainerMetrics{
			{ID: "shop-db", Image: "postgres:17", Project: "shop", Service: "db", Health: "healthy", Restarts: 1, HasSample: true,
				Sample: cgstats.Sample{CPUUsageUsec: 2_500_000, MemCurrent: 1 << 20, MemLimit: 1 << 30, NetRx: 10, NetTx: 20, BlockRead: 30, BlockWrite: 40, PIDs: 5, OOMKills: 3}},
			// A quote and a newline in a label value must be escaped.
			{ID: "odd", Image: "img:\"x\"\nnl"},
		},
	})
	fams := checkExposition(t, b.String())
	want := map[string]string{
		"nyxd_build_info":                   `nyxd_build_info{version="v1.3.0",commit="abc",go_version="`,
		"nyxd_uptime_seconds":               "nyxd_uptime_seconds 90",
		"nyxd_images":                       "nyxd_images 7",
		"nyxd_container_cpu_seconds_total":  `nyxd_container_cpu_seconds_total{name="shop-db",image="postgres:17",compose_project="shop",compose_service="db"} 2.5`,
		"nyxd_container_oom_kills_total":    `{name="shop-db",image="postgres:17",compose_project="shop",compose_service="db"} 3`,
		"nyxd_container_memory_limit_bytes": "} 1.073741824e+09",
	}
	for fam, frag := range want {
		if len(fams[fam]) == 0 || !strings.Contains(strings.Join(fams[fam], "\n"), frag) {
			t.Errorf("%s: want %q in %q", fam, frag, fams[fam])
		}
	}
	if got := fams["nyxd_containers"]; len(got) != 2 || got[0] != `nyxd_containers{state="running"} 2` || got[1] != `nyxd_containers{state="exited"} 2` {
		t.Errorf("containers: %q", got)
	}
	// The unreadable container has no resource samples but still counts restarts.
	if len(fams["nyxd_container_cpu_seconds_total"]) != 1 || len(fams["nyxd_container_restarts_total"]) != 2 {
		t.Errorf("unsampled container: %q / %q", fams["nyxd_container_cpu_seconds_total"], fams["nyxd_container_restarts_total"])
	}
	if !strings.Contains(b.String(), `image="img:\"x\"\nnl"`) {
		t.Errorf("label escaping:\n%s", b.String())
	}
	h := fams["nyxd_container_health_status"]
	if len(h) != 3 || !strings.HasSuffix(h[0], `status="healthy"} 1`) || !strings.HasSuffix(h[1], `status="unhealthy"} 0`) {
		t.Errorf("health: %q", h)
	}

	// Unknown image count: the family is left out.
	b.Reset()
	writeMetrics(&b, metricsInput{images: -1})
	if strings.Contains(b.String(), "nyxd_images") {
		t.Error("nyxd_images without a store")
	}
	checkExposition(t, b.String())
}

func TestMetricsEndpoints(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, "v1", "c", "d", t.TempDir(), "", "", "host", "auto")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.ServeMetrics(ctx, addr); err != nil {
		t.Fatal(err)
	}
	get := func(method, path string) (int, string, string) {
		req, _ := http.NewRequest(method, "http://"+addr+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}
	code, ctype, body := get("GET", "/metrics")
	if code != 200 || ctype != metricsContentType || !strings.Contains(body, `nyxd_containers{state="running"} 0`) {
		t.Fatalf("/metrics: %d %s\n%s", code, ctype, body)
	}
	checkExposition(t, body)
	// Read-only, and nothing else of the API.
	for _, c := range []struct{ method, path string }{{"GET", "/v1/containers"}, {"GET", "/v1/version"}, {"POST", "/metrics"}, {"GET", "/v1/metrics"}} {
		if code, _, _ := get(c.method, c.path); code != 404 && code != 405 {
			t.Errorf("%s %s on the metrics listener: %d", c.method, c.path, code)
		}
	}
	if err := s.ServeMetrics(ctx, addr); err == nil {
		t.Error("second bind on the same address succeeded")
	}
	if err := s.ServeMetrics(ctx, "not an address"); err == nil {
		t.Error("bad address accepted")
	}
}
