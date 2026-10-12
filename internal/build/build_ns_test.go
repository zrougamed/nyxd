//go:build linux

package build_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	goruntime "runtime"

	"github.com/zrougamed/nyxd/internal/build"
	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"golang.org/x/sys/unix"
)

// Builds run for real (RUN in crun, layers committed) in a private user
// namespace with the test image test/app:1 as base.

func tarball(t *testing.T, gz bool, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for name, data := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(data))
	}
	_ = tw.Close()
	if !gz {
		return b.Bytes()
	}
	var z bytes.Buffer
	zw := gzip.NewWriter(&z)
	_, _ = zw.Write(b.Bytes())
	_ = zw.Close()
	return z.Bytes()
}

type env struct {
	t   *testing.T
	d   *daemon.Daemon
	dir string
	out bytes.Buffer
}

func (e *env) write(name, data string) {
	e.t.Helper()
	p := filepath.Join(e.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) build(df string, opts build.Options) (*build.Result, error) {
	e.t.Helper()
	e.write("Dockerfile", df)
	opts.ContextDir = e.dir
	e.out.Reset()
	opts.Out = &e.out
	return e.d.Builder.Build(context.Background(), opts)
}

// runImage runs ref and returns its output.
func (e *env) runImage(ref string, args ...string) string {
	e.t.Helper()
	body := map[string]any{"image": ref, "restart": "no", "id": fmt.Sprintf("r%d", len(e.out.String())+len(ref)+len(args))}
	if len(args) > 0 {
		body["args"] = args
	}
	if code, out := e.d.Do(e.t, "POST", "/v1/containers/run", body); code != 200 {
		e.t.Fatalf("run %s: %d %s", ref, code, out)
	}
	id := body["id"].(string)
	var logs string
	daemon.WaitFor(e.t, 15*1e9, "output of "+ref, func() bool {
		_, logs = e.d.Do(e.t, "GET", "/v1/containers/"+id+"/logs", nil)
		return strings.TrimSpace(logs) != ""
	})
	return logs
}

func TestBuildEveryInstruction(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := daemon.Start(t)
	d.Image(t, "test/app:1", nil)
	e := &env{t: t, d: d, dir: t.TempDir()}

	// Files served for ADD <url>.
	payload := "downloaded payload\n"
	sum := sha256.Sum256([]byte(payload))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files/data.txt", "/":
			w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
			fmt.Fprint(w, payload)
		case "/arch.tar.gz":
			_, _ = w.Write(tarball(t, true, map[string]string{"inurl.txt": "x"}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	e.write("app/hello.txt", "hello file\n")
	e.write("app/sub/deep.txt", "deep\n")
	e.write("glob1.cfg", "1")
	e.write("glob2.cfg", "2")
	if err := os.WriteFile(filepath.Join(e.dir, "files.tar.gz"), tarball(t, true, map[string]string{"fromgz.txt": "gz"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "files.tar"), tarball(t, false, map[string]string{"fromtar.txt": "tar"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if bz, err := exec.LookPath("bzip2"); err == nil {
		raw := tarball(t, false, map[string]string{"frombz.txt": "bz"})
		cmd := exec.Command(bz, "-c")
		cmd.Stdin = bytes.NewReader(raw)
		if out, err := cmd.Output(); err == nil {
			_ = os.WriteFile(filepath.Join(e.dir, "files.tar.bz2"), out, 0o644)
		}
	}
	if err := os.Symlink("hello.txt", filepath.Join(e.dir, "app", "link.txt")); err != nil {
		t.Fatal(err)
	}

	df := `# syntax=docker/dockerfile:1
ARG BASE=test/app:1
ARG UNUSED
FROM ${BASE} AS base
ARG BASE
ARG WHO=world
ARG NOVAL
ENV GREETING="hello $WHO" OTHER=1
ENV OTHER 2
LABEL org.example.a=1 "org.example.b"="two words"
WORKDIR /work
WORKDIR sub
USER root
EXPOSE 80 53/udp
VOLUME ["/data"]
VOLUME /cache /logs
STOPSIGNAL SIGINT
HEALTHCHECK --interval=5s --timeout=2s --start-period=1s --retries=3 CMD ["true"]
SHELL ["/bin/sh", "-c"]
ONBUILD RUN echo onbuild
MAINTAINER someone
RUN echo "$GREETING" > /greeting && echo $OTHER > /other
RUN ["sh", "-c", "pwd > /pwd"]
RUN --network=host true
COPY app/ /app/
COPY --chmod=0600 app/hello.txt /secret.txt
COPY --chown=1000:1000 --link glob*.cfg /cfg/
COPY app/link.txt /linkcopy.txt
ADD files.tar.gz /x/
ADD files.tar /x/
COPY app/hello.txt rel/
ADD ` + srv.URL + `/files/data.txt /dl/
ADD --checksum=sha256:` + hex.EncodeToString(sum[:]) + ` ` + srv.URL + `/ /dl/root.txt
ADD ` + srv.URL + `/arch.tar.gz /dl/
ADD ` + srv.URL + ` /dl2/
COPY --chmod=4755 app/hello.txt /suid
COPY --chmod=2755 app/hello.txt /sgid
COPY --chmod=1777 app/sub /sticky
CMD echo shell-form-cmd

FROM base AS second
ENTRYPOINT ["cat"]
CMD ["/greeting"]

FROM scratch AS empty
COPY --from=base /greeting /g

FROM 1
COPY --from=0 /other /other2
COPY --from=empty /g /g2
COPY --from=test/app:1 /etc/passwd /passwd.copy
ENTRYPOINT ["sh", "-c"]
CMD ["cat /greeting /other2 /g2 /app/hello.txt /app/sub/deep.txt /secret.txt /cfg/glob1.cfg /x/fromgz.txt /x/fromtar.txt /dl/data.txt /dl/root.txt /linkcopy.txt"]
`
	res, err := e.build(df, build.Options{Tags: []string{"test/all:1"}, BuildArgs: map[string]string{"WHO": "nyx", "NOTDECLARED": "x"}})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, e.out.String())
	}
	if !strings.Contains(e.out.String(), `"NOTDECLARED" was not consumed`) || !strings.Contains(e.out.String(), "ONBUILD") {
		t.Errorf("warnings missing:\n%s", e.out.String())
	}
	out := e.runImage("test/all:1")
	for _, want := range []string{"hello nyx", "hello file", "deep", "gz", "tar", "downloaded payload"} {
		if !strings.Contains(out, want) {
			t.Errorf("image output lacks %q:\n%s", want, out)
		}
	}
	m, cfg, err := d.Store.LoadImageMeta("test/all:1")
	if err != nil || len(m.Layers) < 2 || res.ImageID != m.Config.Digest {
		t.Fatalf("image meta: %v %+v", err, res)
	}
	if cfg.Config.WorkingDir != "/work/sub" || cfg.Config.StopSignal != "SIGINT" || cfg.Config.Healthcheck == nil ||
		cfg.Config.Labels["org.example.b"] != "two words" || len(cfg.Config.ExposedPorts) != 2 || len(cfg.Config.Volumes) != 3 {
		t.Errorf("config: %+v", cfg.Config)
	}

	// Rebuild: every step is cached.
	if _, err := e.build(df, build.Options{Tags: []string{"test/all:2"}, BuildArgs: map[string]string{"WHO": "nyx"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "cache") {
		t.Errorf("no cache hits on rebuild:\n%s", e.out.String())
	}
	// --target and --no-cache.
	if _, err := e.build(df, build.Options{Target: "second", NoCache: true, BuildArgs: map[string]string{"WHO": "t"}}); err != nil {
		t.Fatal(err)
	}
	// A base image with ONBUILD triggers (warned, not run); bzip2 archives.
	if _, err := e.build("FROM test/app:1\nONBUILD RUN false\n", build.Options{Tags: []string{"test/ob:1"}}); err != nil {
		t.Fatal(err)
	}
	bz := ""
	if _, err := os.Stat(filepath.Join(e.dir, "files.tar.bz2")); err == nil {
		bz = "ADD files.tar.bz2 /x/\n"
	}
	if _, err := e.build("FROM test/ob:1\n"+bz, build.Options{}); err != nil || !strings.Contains(e.out.String(), "ONBUILD triggers") {
		t.Errorf("onbuild base: %v\n%s", err, e.out.String())
	}
	// A FIFO in the context is skipped.
	if err := syscall.Mkfifo(filepath.Join(e.dir, "app", "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.build("FROM test/app:1\nCOPY app /app2\n", build.Options{}); err != nil {
		t.Errorf("context with a fifo: %v", err)
	}
	_ = os.Remove(filepath.Join(e.dir, "app", "fifo"))
	// HEALTHCHECK NONE and shell form, an ENTRYPOINT without inherited CMD, a USER from the image.
	if _, err := e.build("FROM test/all:1\nHEALTHCHECK NONE\nENTRYPOINT echo e\nUSER 0\nRUN id > /tmp/uid\n", build.Options{Tags: []string{"test/hc:1"}}); err != nil {
		t.Fatalf("build: %v\n%s", err, e.out.String())
	}
	if _, err := e.build("FROM test/app:1\nHEALTHCHECK CMD true\n", build.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildFailures(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := daemon.Start(t)
	d.Image(t, "test/app:1", nil)
	e := &env{t: t, d: d, dir: t.TempDir()}
	e.write("a.txt", "a")
	for want, df := range map[string]string{
		"unknown instruction":             "FROM test/app:1\nFROBNICATE x\n",
		"did not complete successfully":   "FROM test/app:1\nRUN false\n",
		"--mount is not supported":        "FROM test/app:1\nRUN --mount=type=cache,target=/c true\n",
		"unsupported flag":                "FROM test/app:1\nRUN --bogus true\n",
		"SHELL requires the JSON form":    "FROM test/app:1\nSHELL /bin/sh -c\n",
		"expected HEALTHCHECK":            "FROM test/app:1\nHEALTHCHECK echo\n",
		"--interval":                      "FROM test/app:1\nHEALTHCHECK --interval=x CMD true\n",
		"--retries":                       "FROM test/app:1\nHEALTHCHECK --retries=-1 CMD true\n",
		"ADD does not support --from":     "FROM test/app:1\nADD --from=x a.txt /\n",
		"unsupported flag --bogus":        "FROM test/app:1\nCOPY --bogus a.txt /\n",
		"requires at least one source":    "FROM test/app:1\nCOPY a.txt\n",
		"expected an octal mode":          "FROM test/app:1\nCOPY --chmod=rwx a.txt /\n",
		"COPY does not take URLs":         "FROM test/app:1\nCOPY http://example.com/x /\n",
		"git sources are not supported":   "FROM test/app:1\nADD git@github.com:x/y.git /\n",
		"--checksum is only supported":    "FROM test/app:1\nADD --checksum=sha256:00 a.txt /\n",
		"--chown=nosuchuser":              "FROM test/app:1\nCOPY --chown=nosuchuser a.txt /\n",
		"missing.txt":                     "FROM test/app:1\nCOPY missing.txt /\n",
		"--from=nostage":                  "FROM test/app:1\nCOPY --from=nostage /x /\n",
		"only linux/":                     "FROM --platform=linux/s390x test/app:1\n",
		"target stage \"nope\" not found": "FROM test/app:1\n",
		"ARG requires a name":             "FROM test/app:1\nARG\n",
		"empty name":                      "ARG =x\nFROM test/app:1\n",
		"pull":                            "FROM registry.invalid/x:1\n",
		"empty command":                   "FROM test/app:1\nRUN []\n",
		"no source files":                 "FROM test/app:1\nCOPY nomatch*.txt /\n",
		"syntax error in pattern":         "FROM test/app:1\nCOPY [ /\n",
		"is not a directory":              "FROM test/app:1\nCOPY a.txt /etc/passwd/\n",
		"no FROM":                         "ARG X=1\n",
	} {
		opts := build.Options{}
		if strings.Contains(want, "target stage") {
			opts.Target = "nope"
		}
		if _, err := e.build(df, opts); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", want, err)
		}
	}
	// Bad variable syntax wherever words are expanded.
	for _, ins := range []string{"ENV A=${x", "LABEL a=${x", "WORKDIR ${x", "USER ${x", "EXPOSE ${x", "VOLUME ${x",
		"STOPSIGNAL ${x", "ARG A=${x", "COPY ${x /", "COPY --chown=${x a.txt /", "COPY --from=${x /a /", "COPY --chmod=${x a.txt /"} {
		if _, err := e.build("FROM test/app:1\n"+ins+"\n", build.Options{}); err == nil {
			t.Errorf("%s: accepted", ins)
		}
	}
	for _, df := range []string{"FROM ${x\n", "FROM --platform=${x test/app:1\n", "ARG A=${x\nFROM test/app:1\n"} {
		if _, err := e.build(df, build.Options{}); err == nil {
			t.Errorf("%q: accepted", df)
		}
	}
	// Meta ARG given on the command line; --platform matching the host.
	if _, err := e.build("ARG BASE=nope\nFROM --platform=$BUILDPLATFORM ${BASE}\n", build.Options{BuildArgs: map[string]string{"BASE": "test/app:1"}}); err != nil {
		t.Errorf("meta arg from build args: %v", err)
	}
	if _, err := e.build("FROM --platform=linux/"+goruntime.GOARCH+" test/app:1\n", build.Options{}); err != nil {
		t.Errorf("host platform: %v", err)
	}

	// Dockerfile outside the context, and missing.
	if _, err := d.Builder.Build(context.Background(), build.Options{ContextDir: e.dir, Dockerfile: "../x"}); err == nil {
		t.Error("Dockerfile outside the context")
	}
	if _, err := d.Builder.Build(context.Background(), build.Options{ContextDir: e.dir, Dockerfile: "nope"}); err == nil {
		t.Error("missing Dockerfile")
	}
	e.write("Dockerfile", "FROM test/app:1\nRUN <<EOF\necho\nEOF\n")
	if _, err := d.Builder.Build(context.Background(), build.Options{ContextDir: e.dir}); err == nil {
		t.Error("heredoc accepted")
	}

	// ADD <url> failures.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "data")
	}))
	defer srv.Close()
	if _, err := e.build("FROM test/app:1\nADD "+srv.URL+"/missing /x\n", build.Options{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404 url: %v", err)
	}
	if _, err := e.build("FROM test/app:1\nADD --checksum=sha256:"+strings.Repeat("0", 64)+" "+srv.URL+"/f /x\n", build.Options{}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("bad checksum: %v", err)
	}
	if _, err := e.build("FROM test/app:1\nADD http://127.0.0.1:1/x /x\n", build.Options{}); err == nil {
		t.Error("unreachable url")
	}
}

func TestBuildReadOnlyCache(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := daemon.Start(t)
	d.Image(t, "test/app:1", nil)
	e := &env{t: t, d: d, dir: t.TempDir()}
	e.write("a.txt", "a")
	cache := filepath.Join(d.Base, "build", "cache")
	_ = os.MkdirAll(cache, 0o700)
	if err := unix.Mount(cache, cache, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("", cache, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(cache, unix.MNT_DETACH) //nolint:errcheck
	// Cache writes fail; the build still succeeds.
	if _, err := e.build("FROM test/app:1\nCOPY a.txt /a\nRUN touch /b\n", build.Options{Tags: []string{"test/ro:1"}}); err != nil {
		t.Fatalf("build with a read-only cache: %v", err)
	}
}
