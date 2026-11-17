package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/supervisor"
)

// Metrics in the Prometheus text format (version 0.0.4), written by hand from
// the counters nyx stats already reads: no client library. Served on the
// control socket at GET /v1/metrics and, with nyxd -metrics-listen, on a TCP
// listener that serves /metrics and nothing else.

const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", metricsContentType)
	var containers []supervisor.ContainerMetrics
	exited := 0
	if s.sup != nil {
		containers = s.sup.Metrics(r.Context())
		exited = len(s.sup.ExitedInfo())
	}
	images := -1
	if s.store != nil {
		if refs, err := s.store.ListImageRefs(); err == nil {
			images = len(refs)
		}
	}
	writeMetrics(w, metricsInput{
		version: s.version, commit: s.gitCommit,
		uptime:     time.Since(s.started),
		containers: containers, exited: exited, images: images,
	})
}

type metricsInput struct {
	version, commit string
	uptime          time.Duration
	containers      []supervisor.ContainerMetrics
	exited          int
	images          int // -1: unknown
}

// promWriter writes metric families: HELP and TYPE once, then the samples.
type promWriter struct{ w io.Writer }

func (p promWriter) family(name, typ, help string) {
	fmt.Fprintf(p.w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func (p promWriter) sample(name, labels string, v float64) {
	if labels != "" {
		labels = "{" + labels + "}"
	}
	fmt.Fprintf(p.w, "%s%s %s\n", name, labels, strconv.FormatFloat(v, 'g', -1, 64))
}

// label renders k="v" pairs with Prometheus escaping (backslash, quote,
// newline); pairs with an empty value are left out.
func label(kv ...string) string {
	var parts []string
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			continue
		}
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(kv[i+1])
		parts = append(parts, kv[i]+`="`+v+`"`)
	}
	return strings.Join(parts, ",")
}

func writeMetrics(w io.Writer, in metricsInput) {
	p := promWriter{w}
	p.family("nyxd_build_info", "gauge", "nyxd version, commit and Go version (value is always 1).")
	p.sample("nyxd_build_info", label("version", in.version, "commit", in.commit, "go_version", goruntime.Version()), 1)
	p.family("nyxd_uptime_seconds", "gauge", "Seconds since nyxd started.")
	p.sample("nyxd_uptime_seconds", "", in.uptime.Seconds())
	p.family("nyxd_containers", "gauge", "Containers by state.")
	p.sample("nyxd_containers", label("state", "running"), float64(len(in.containers)))
	p.sample("nyxd_containers", label("state", "exited"), float64(in.exited))
	if in.images >= 0 {
		p.family("nyxd_images", "gauge", "Images in the local store.")
		p.sample("nyxd_images", "", float64(in.images))
	}

	ls := make([]string, len(in.containers))
	for i, c := range in.containers {
		ls[i] = label("name", c.ID, "image", c.Image, "compose_project", c.Project, "compose_service", c.Service)
	}
	each := func(name, typ, help string, sampled bool, value func(supervisor.ContainerMetrics) float64) {
		p.family(name, typ, help)
		for i, c := range in.containers {
			if sampled && !c.HasSample {
				continue
			}
			p.sample(name, ls[i], value(c))
		}
	}
	each("nyxd_container_cpu_seconds_total", "counter", "CPU time used by the container.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.CPUUsageUsec) / 1e6 })
	each("nyxd_container_memory_usage_bytes", "gauge", "Memory in use by the container.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.MemCurrent) })
	each("nyxd_container_memory_limit_bytes", "gauge", "Memory limit of the container (host memory when unlimited).", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.MemLimit) })
	each("nyxd_container_network_receive_bytes_total", "counter", "Bytes received by the container.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.NetRx) })
	each("nyxd_container_network_transmit_bytes_total", "counter", "Bytes sent by the container.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.NetTx) })
	each("nyxd_container_block_read_bytes_total", "counter", "Bytes read from block devices by the container.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.BlockRead) })
	each("nyxd_container_block_write_bytes_total", "counter", "Bytes written to block devices by the container.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.BlockWrite) })
	each("nyxd_container_pids", "gauge", "Processes in the container.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.PIDs) })
	each("nyxd_container_oom_kills_total", "counter", "Processes in the container killed by the OOM killer.", true,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Sample.OOMKills) })
	each("nyxd_container_restarts_total", "counter", "Restarts by the restart policy since the container was started.", false,
		func(c supervisor.ContainerMetrics) float64 { return float64(c.Restarts) })

	p.family("nyxd_container_health_status", "gauge", "Health check status of the container (1 for the current status).")
	for i, c := range in.containers {
		if c.Health == "" {
			continue
		}
		for _, st := range []string{"healthy", "unhealthy", "starting"} {
			v := 0.0
			if c.Health == st {
				v = 1
			}
			p.sample("nyxd_container_health_status", ls[i]+","+label("status", st), v)
		}
	}

	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	p.family("go_goroutines", "gauge", "Goroutines in nyxd.")
	p.sample("go_goroutines", "", float64(goruntime.NumGoroutine()))
	p.family("go_memstats_heap_alloc_bytes", "gauge", "Heap bytes allocated and in use.")
	p.sample("go_memstats_heap_alloc_bytes", "", float64(ms.HeapAlloc))
	p.family("go_memstats_sys_bytes", "gauge", "Bytes obtained from the system by the Go runtime.")
	p.sample("go_memstats_sys_bytes", "", float64(ms.Sys))
	p.family("go_gc_cycles_total", "counter", "Completed GC cycles.")
	p.sample("go_gc_cycles_total", "", float64(ms.NumGC))
}

// ServeMetrics serves /metrics (and nothing else) on addr until ctx ends,
// for scrapers that cannot read a Unix socket (nyxd -metrics-listen). The
// address is bound before it returns, so a bad address fails nyxd's start.
func (s *Server) ServeMetrics(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("metrics listener", "addr", addr, "err", err)
		}
	}()
	s.log.Info("metrics listening", "addr", ln.Addr().String())
	return nil
}
