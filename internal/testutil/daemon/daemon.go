//go:build linux

// Package daemon starts the nyxd stack in-process for tests: image store,
// overlay, crun, host networking, logs, supervisor, builder and the control
// API on a Unix socket, wired as cmd/nyxd does in rootless single-ID mode.
// Call it from inside nsrun.Run; it needs crun on PATH.
//
// Images come from Image: one layer holding internal/testutil/testapp (a
// multi-call binary standing in for sh, echo, cat, sleep, ...), so tests need
// no registry and no network.
package daemon

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/build"
	"github.com/zrougamed/nyxd/internal/control"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/logs"
	"github.com/zrougamed/nyxd/internal/netdns"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/overlay"
	nyxrt "github.com/zrougamed/nyxd/internal/runtime"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/pkg/oci"
	"golang.org/x/sys/unix"
)

// Daemon is a running in-process nyxd.
type Daemon struct {
	Base    string // -base-dir
	Socket  string // control API socket
	Store   *image.Store
	Overlay *overlay.Manager
	Runtime *nyxrt.Runtime
	Logs    *logs.Collector
	Sup     *supervisor.Supervisor
	Ctl     *control.Server
	Builder *build.Builder
	Log     *bytes.Buffer // daemon log (JSON lines)
	Ctx     context.Context
}

// Apps are the commands the test image provides in /bin (see testapp).
var Apps = []string{"sh", "echo", "cat", "sleep", "id", "true", "false", "env", "pwd", "touch", "mkdir",
	"hostname", "ignoreterm", "trapterm", "serve", "chatty"}

// Start starts a daemon under t.TempDir(); it is stopped at cleanup.
func Start(t *testing.T) *Daemon {
	t.Helper()
	if _, err := exec.LookPath("crun"); err != nil {
		t.Skip("crun not installed")
	}
	base := t.TempDir()
	logBuf := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := &Daemon{Base: base, Socket: filepath.Join(base, "nyxd.sock"), Log: &logBuf.Buffer}

	var err error
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	d.Store, err = image.NewStore(base + "/images")
	must(err)
	d.Overlay, err = overlay.NewManager(base+"/overlay", overlay.Options{Rootless: true, SingleID: true})
	must(err)
	d.Runtime, err = nyxrt.New("crun", base+"/run/crun")
	must(err)
	d.Runtime.SetCgroupManager("disabled")
	d.Logs, err = logs.NewCollector(base+"/logs", log)
	must(err)
	d.Sup = supervisor.New(d.Runtime, d.Overlay, network.HostBackend{}, base, log, d.Logs, d.Store,
		netdns.Noop{}, "host", "auto", "")
	d.Sup.SetRootless(true, false)

	ctx, cancel := context.WithCancel(context.Background())
	d.Ctx = ctx
	d.Ctl = control.New(log, d.Runtime, d.Store, d.Sup, ctx, nil, "test", "abc1234", "2026-10-11",
		base, d.Socket, "", "host", "auto")
	d.Builder, err = build.New(build.Config{Store: d.Store, Overlay: d.Overlay, Runtime: d.Runtime,
		BaseDir: base, Rootless: true, SingleID: true, Log: log})
	must(err)
	d.Ctl.SetBuilder(d.Builder)
	must(d.Ctl.Start())

	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer scancel()
		_ = d.Ctl.Shutdown(sctx)
		d.Sup.Shutdown(sctx)
		cancel()
		unmountUnder(base)
		if t.Failed() {
			t.Logf("daemon log:\n%s", logBuf.String())
		}
	})
	return d
}

// Client is an HTTP client for the control socket (requests use http://unix/...).
func (d *Daemon) Client() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dl net.Dialer
			return dl.DialContext(ctx, "unix", d.Socket)
		},
	}}
}

// Do sends a request with an optional JSON body and returns status and body.
func (d *Daemon) Do(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://unix"+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Image stores ref: the test image, with mutate applied to its config.
// Default command: echo hello.
func (d *Daemon) Image(t *testing.T, ref string, mutate func(*oci.ImageConfig)) {
	t.Helper()
	cfg := &oci.ImageConfig{
		Architecture: runtime.GOARCH,
		OS:           "linux",
		Config: oci.ContainerConfig{
			Env: []string{"PATH=/bin"},
			Cmd: []string{"echo", "hello"},
		},
	}
	if mutate != nil {
		mutate(cfg)
	}
	if err := StoreImage(d.Store, ref, cfg, nil); err != nil {
		t.Fatal(err)
	}
}

// StoreImage writes the test image under ref into store; extra files are
// added to the layer (path -> content).
func StoreImage(store *image.Store, ref string, cfg *oci.ImageConfig, extra map[string]string) error {
	layerTar, err := Layer(extra)
	if err != nil {
		return err
	}
	diff := sha256.Sum256(layerTar)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(layerTar); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	layerDigest, err := store.PutBlob(gz.Bytes())
	if err != nil {
		return err
	}
	cfg.RootFS = oci.RootFS{Type: "layers", DiffIDs: []string{"sha256:" + hex.EncodeToString(diff[:])}}
	cfgDigest, cfgData, err := image.ConfigDigest(cfg)
	if err != nil {
		return err
	}
	if _, err := store.PutBlob(cfgData); err != nil {
		return err
	}
	m := &oci.Manifest{
		SchemaVersion: 2,
		MediaType:     oci.MediaTypeImageManifest,
		Config:        oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: cfgDigest, Size: int64(len(cfgData))},
		Layers:        []oci.Descriptor{{MediaType: oci.MediaTypeImageLayerGzip, Digest: layerDigest, Size: int64(gz.Len())}},
	}
	return store.SaveImage(ref, m, cfg)
}

// Layer returns the test image's root filesystem as an uncompressed tar.
func Layer(extra map[string]string) ([]byte, error) {
	app, err := TestApp()
	if err != nil {
		return nil, err
	}
	bin, err := os.ReadFile(app)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	mt := time.Unix(1700000000, 0)
	dir := func(name string, mode int64) {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: mode, ModTime: mt})
	}
	file := func(name string, mode int64, data []byte) {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(data)), ModTime: mt})
		_, _ = tw.Write(data)
	}
	for _, d := range []string{"bin", "etc", "root", "proc", "sys", "dev", "run", "home", "home/app"} {
		dir(d, 0o755)
	}
	dir("tmp", 0o1777)
	file("bin/testapp", 0o755, bin)
	for _, a := range Apps {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: "bin/" + a, Linkname: "testapp", Mode: 0o777, ModTime: mt})
	}
	file("etc/passwd", 0o644, []byte("root:x:0:0:root:/root:/bin/sh\napp:x:1000:1000:app:/home/app:/bin/sh\n"))
	file("etc/group", 0o644, []byte("root:x:0:\napp:x:1000:\n"))
	names := make([]string, 0, len(extra))
	for n := range extra {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		file(strings.TrimPrefix(n, "/"), 0o644, []byte(extra[n]))
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	appOnce sync.Once
	appPath string
	appErr  error
)

// TestApp builds internal/testutil/testapp (static) once per process.
func TestApp() (string, error) {
	appOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nyxd-testapp-")
		if err != nil {
			appErr = err
			return
		}
		appPath = filepath.Join(dir, "testapp")
		cmd := exec.Command("go", "build", "-o", appPath, "github.com/zrougamed/nyxd/internal/testutil/testapp")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			appErr = &buildError{out: string(out), err: err}
		}
	})
	return appPath, appErr
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return "build testapp: " + e.err.Error() + ": " + e.out }

// unmountUnder detaches every mount below dir (deepest first) so the test's
// temporary directory can be removed.
func unmountUnder(dir string) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return
	}
	defer f.Close()
	var mps []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 && strings.HasPrefix(fields[4], dir) {
			mps = append(mps, fields[4])
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(mps)))
	for _, mp := range mps {
		_ = unix.Unmount(mp, unix.MNT_DETACH)
	}
}

// WaitFor polls cond every 20ms until it is true or d passes.
func WaitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}
