package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// scripted is a registry whose behaviour each test sets: manifests by
// reference, blobs, auth challenge, Range support and failures.
type scripted struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	manifests map[string]served // "<ref>" (tag or digest) -> answer
	blobs     map[string][]byte
	exists    map[string]bool // HEAD answers 200 for these digests

	v2Status    int    // GET /v2/ status (default 200)
	challenge   string // sent with 401 when the request has no Authorization
	tokenStatus int    // token endpoint status (default 200)
	tokenBody   string // token endpoint body
	rangeMode   string // "", "ignore" (200 full body), "error" (416)
	blobStatus  int    // non-zero: every blob GET answers this
	corrupt     bool   // blobs are served with a wrong byte

	uploadStatus   int    // POST /blobs/uploads/ status (default 202)
	uploadLocation string // Location for the upload ("" = relative default)
	putStatus      int    // PUT upload status (default 201)
	headStatus     int    // non-zero: HEAD answers this
	manifestPut    int    // manifest PUT status (default 201)
}

type served struct {
	ct     string
	body   []byte
	status int
}

func newScripted(t *testing.T) *scripted {
	s := &scripted{t: t, manifests: map[string]served{}, blobs: map[string][]byte{}, exists: map[string]bool{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *scripted) host() string { return strings.TrimPrefix(s.srv.URL, "http://") }

func (s *scripted) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path == "/token" {
		if s.tokenStatus != 0 {
			w.WriteHeader(s.tokenStatus)
			return
		}
		fmt.Fprint(w, s.tokenBody)
		return
	}
	if s.challenge != "" && r.Header.Get("Authorization") == "" {
		w.Header().Set("WWW-Authenticate", strings.ReplaceAll(s.challenge, "REALM", s.srv.URL+"/token"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/v2/")
	switch {
	case r.URL.Path == "/v2/":
		if s.v2Status != 0 {
			w.WriteHeader(s.v2Status)
		}
	case strings.Contains(p, "/manifests/"):
		ref := p[strings.Index(p, "/manifests/")+len("/manifests/"):]
		if r.Method == http.MethodPut {
			io.Copy(io.Discard, r.Body) //nolint:errcheck
			w.WriteHeader(or(s.manifestPut, http.StatusCreated))
			return
		}
		m, ok := s.manifests[ref]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if m.ct != "" {
			w.Header().Set("Content-Type", m.ct)
		}
		w.WriteHeader(or(m.status, http.StatusOK))
		w.Write(m.body) //nolint:errcheck
	case strings.HasSuffix(p, "/blobs/uploads/") && r.Method == http.MethodPost:
		loc := s.uploadLocation
		if loc == "" {
			loc = "/v2/" + strings.TrimSuffix(p, "/blobs/uploads/") + "/blobs/uploads/u1?_state=x"
		}
		if loc != "none" {
			w.Header().Set("Location", loc)
		}
		w.WriteHeader(or(s.uploadStatus, http.StatusAccepted))
	case strings.Contains(p, "/blobs/uploads/") && r.Method == http.MethodPut:
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		if r.URL.Query().Get("_state") != "x" && s.uploadLocation == "" {
			http.Error(w, "upload state lost", http.StatusBadRequest)
			return
		}
		w.WriteHeader(or(s.putStatus, http.StatusCreated))
	case strings.Contains(p, "/blobs/"):
		d := p[strings.LastIndex(p, "/")+1:]
		if r.Method == http.MethodHead {
			switch {
			case s.headStatus != 0:
				w.WriteHeader(s.headStatus)
			case s.exists[d]:
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		if s.blobStatus != 0 {
			w.WriteHeader(s.blobStatus)
			return
		}
		data, ok := s.blobs[d]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if s.corrupt {
			data = append([]byte{'X'}, data[1:]...)
		}
		if rg := r.Header.Get("Range"); rg != "" {
			switch s.rangeMode {
			case "ignore":
			case "error":
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			default:
				start, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(data[start:]) //nolint:errcheck
				return
			}
		}
		w.Write(data) //nolint:errcheck
	default:
		http.NotFound(w, r)
	}
}

func or(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// publish copies image ref from src into the registry under tag.
func (s *scripted) publish(t *testing.T, src *Store, ref, tag string) *oci.Manifest {
	t.Helper()
	m, _, err := src.LoadImageMeta(ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range append([]oci.Descriptor{m.Config}, m.Layers...) {
		b, err := os.ReadFile(src.BlobPath(d.Digest))
		if err != nil {
			t.Fatal(err)
		}
		s.blobs[d.Digest] = b
	}
	body, _ := json.Marshal(m)
	s.manifests[tag] = served{ct: oci.MediaTypeImageManifest, body: body}
	return m
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPullScenarios(t *testing.T) {
	src := newStore(t)
	seedImage(t, src, "src/app:1")
	ctx := context.Background()

	t.Run("index picks the platform", func(t *testing.T) {
		r := newScripted(t)
		m := r.publish(t, src, "src/app:1", "sha256:arm")
		idx, _ := json.Marshal(oci.Index{SchemaVersion: 2, Manifests: []oci.Descriptor{
			{Digest: "sha256:none"},
			{Digest: "sha256:amd", Platform: &oci.Platform{OS: "linux", Architecture: "amd64"}},
			{Digest: "sha256:arm", Platform: &oci.Platform{OS: "linux", Architecture: "arm64"}},
		}})
		r.manifests["1"] = served{ct: oci.MediaTypeImageIndex, body: idx}
		r.manifests["2"] = served{ct: oci.MediaTypeDockerManifestList, body: idx}
		s := newStore(t)
		s.SetPlatform("linux/arm64")
		if _, err := s.PullWithProgress(ctx, r.host()+"/app:1", nil, nil); err != nil {
			t.Fatal(err)
		}
		// LoadManifest reads a manifest kept as a blob.
		d, err := s.PutBlob(r.manifests["sha256:arm"].body)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.LoadManifest(d); err != nil || got.Config.Digest != m.Config.Digest {
			t.Fatalf("LoadManifest: %v", err)
		}
		if _, err := s.LoadManifest("sha256:missing"); err == nil {
			t.Error("LoadManifest of a missing blob")
		}
		junk, _ := s.PutBlob([]byte("{junk"))
		if _, err := s.LoadManifest(junk); err == nil {
			t.Error("LoadManifest of a non-manifest")
		}
		s.SetPlatform("linux/s390x")
		if _, err := s.PullWithProgress(ctx, r.host()+"/app:2", nil, nil); err == nil || !strings.Contains(err.Error(), "no manifest in index") {
			t.Errorf("unmatched platform: %v", err)
		}
		s.SetPlatform("")
	})

	t.Run("broken manifests", func(t *testing.T) {
		r := newScripted(t)
		r.manifests["html"] = served{ct: "text/html", body: []byte("<html>login</html>")}
		r.manifests["empty"] = served{ct: oci.MediaTypeImageManifest}
		r.manifests["junk"] = served{ct: oci.MediaTypeImageManifest, body: []byte("{junk")}
		r.manifests["badidx"] = served{ct: oci.MediaTypeImageIndex, body: []byte("{junk")}
		r.manifests["500"] = served{status: 500}
		s := newStore(t)
		for tag, want := range map[string]string{"html": "HTML/XML", "empty": "empty body", "junk": "content-type", "badidx": "content-type", "500": "500", "missing": "404"} {
			if _, err := s.PullWithProgress(ctx, r.host()+"/app:"+tag, nil, nil); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v", tag, err)
			}
		}
		r.v2Status = 503
		if _, err := s.PullWithProgress(ctx, r.host()+"/app:html", nil, nil); err == nil || !strings.Contains(err.Error(), "503") {
			t.Errorf("v2 down: %v", err)
		}
	})

	t.Run("auth challenges", func(t *testing.T) {
		r := newScripted(t)
		r.publish(t, src, "src/app:1", "1")
		s := newStore(t)
		pull := func(auth *RegistryAuth) error {
			_ = s.RemoveImage(r.host() + "/app:1")
			_, err := s.PullWithProgress(ctx, r.host()+"/app:1", auth, nil)
			return err
		}
		r.challenge = `Basic realm="x"`
		if err := pull(nil); err == nil || !strings.Contains(err.Error(), "requires credentials") {
			t.Errorf("basic without credentials: %v", err)
		}
		if err := pull(&RegistryAuth{Username: "u", Password: "p"}); err != nil {
			t.Errorf("basic: %v", err)
		}
		r.challenge = `Bearer realm="REALM",service="svc"`
		r.tokenBody = `{"access_token":"t"}`
		if err := pull(&RegistryAuth{Username: "u", Password: "p"}); err != nil {
			t.Errorf("bearer access_token: %v", err)
		}
		for body, want := range map[string]string{`{}`: "empty token", `{bad`: "token response"} {
			r.tokenBody = body
			if err := pull(nil); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("token %s: %v", body, err)
			}
		}
		r.tokenStatus = 500
		if err := pull(nil); err == nil || !strings.Contains(err.Error(), "token request") {
			t.Errorf("token 500: %v", err)
		}
		r.tokenStatus = 0
		for ch, want := range map[string]string{`Bearer service="x"`: "without realm", `Bearer realm="::bad"`: "bad realm", `Negotiate x`: "unsupported auth"} {
			r.challenge = ch
			if err := pull(nil); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v", ch, err)
			}
		}
		r.challenge = `Bearer realm="http://127.0.0.1:1/token"`
		if err := pull(nil); err == nil {
			t.Error("unreachable token server")
		}
	})

	t.Run("blob failures and resume", func(t *testing.T) {
		r := newScripted(t)
		m := r.publish(t, src, "src/app:1", "1")
		layer := m.Layers[0]
		s := newStore(t)
		ref := r.host() + "/app:1"
		r.blobStatus = 500
		if _, err := s.PullWithProgress(ctx, ref, nil, nil); err == nil {
			t.Error("blob 500")
		}
		r.blobStatus = 0
		r.corrupt = true
		if _, err := s.PullWithProgress(ctx, ref, nil, nil); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Errorf("corrupt blob: %v", err)
		}
		r.corrupt = false

		data := r.blobs[layer.Digest]
		partial := s.BlobPath(layer.Digest) + ".partial"
		resume := func(mode string, part []byte) error {
			_ = os.Remove(s.BlobPath(layer.Digest))
			_ = s.RemoveImage(ref)
			if err := os.MkdirAll(s.root+"/blobs/sha256", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(partial, part, 0o600); err != nil {
				t.Fatal(err)
			}
			r.rangeMode = mode
			var events int
			_, err := s.PullWithProgress(ctx, ref, nil, func(PullEvent) { events++ })
			return err
		}
		for _, mode := range []string{"", "ignore"} {
			if err := resume(mode, data[:len(data)/2]); err != nil {
				t.Errorf("resume %q: %v", mode, err)
			}
		}
		if err := resume("error", data[:len(data)/2]); err == nil || !strings.Contains(err.Error(), "416") {
			t.Errorf("range refused: %v", err)
		}
		if err := resume("", append(append([]byte{}, data...), 'x')); err != nil {
			t.Errorf("oversized partial: %v", err)
		}
		if err := resume("", data); err != nil {
			t.Errorf("complete partial: %v", err)
		}
		bad := append([]byte{'X'}, data[1:]...)
		if err := resume("", bad); err == nil || !strings.Contains(err.Error(), "partial") {
			t.Errorf("complete but wrong partial: %v", err)
		}
		if err := resume("", append([]byte{'X'}, data[1:len(data)/2]...)); err == nil {
			t.Error("resumed onto a wrong prefix")
		}
	})
}

func TestPushScenarios(t *testing.T) {
	s := newStore(t)
	seedImage(t, s, "src/app:1")
	ctx := context.Background()
	push := func(r *scripted) error {
		if err := s.Tag("src/app:1", r.host()+"/app:1"); err != nil {
			t.Fatal(err)
		}
		var events []string
		_, err := s.Push(ctx, r.host()+"/app:1", &RegistryAuth{Username: "u"}, func(ev PullEvent) { events = append(events, ev.Phase) })
		return err
	}
	r := newScripted(t)
	if err := push(r); err != nil {
		t.Fatalf("push: %v", err)
	}
	r = newScripted(t)
	r.headStatus = 200 // everything exists
	if err := push(r); err != nil {
		t.Errorf("push with existing blobs: %v", err)
	}
	for name, set := range map[string]func(*scripted){
		"head 500":        func(r *scripted) { r.headStatus = 500 },
		"post 500":        func(r *scripted) { r.uploadStatus = 500 },
		"no location":     func(r *scripted) { r.uploadLocation = "none" },
		"bad location":    func(r *scripted) { r.uploadLocation = "http://[::1" },
		"put 400":         func(r *scripted) { r.putStatus = 400 },
		"manifest 400":    func(r *scripted) { r.manifestPut = 400 },
		"absolute loc ok": func(r *scripted) { r.uploadLocation = r.srv.URL + "/v2/app/blobs/uploads/abs" },
	} {
		r := newScripted(t)
		set(r)
		err := push(r)
		if (err == nil) != (name == "absolute loc ok") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.Push(ctx, "x@sha256:abc", nil, nil); err == nil || !strings.Contains(err.Error(), "needs a tag") {
		t.Errorf("digest ref: %v", err)
	}
	if _, err := s.Push(ctx, "nope:1", nil, nil); err == nil {
		t.Error("unknown image")
	}
	// A layer blob missing locally.
	m, _, _ := s.LoadImageMeta("src/app:1")
	r = newScripted(t)
	_ = s.Tag("src/app:1", r.host()+"/gone:1")
	_ = os.Remove(s.BlobPath(m.Layers[0].Digest))
	if _, err := s.Push(ctx, r.host()+"/gone:1", nil, nil); err == nil || !strings.Contains(err.Error(), "missing from the local store") {
		t.Errorf("missing blob: %v", err)
	}
}

func TestSmallHelpers(t *testing.T) {
	if shortID("sha256:0123456789abcdef") != "0123456789ab" || shortID("x") != "x" {
		t.Errorf("shortID %q", shortID("sha256:0123456789abcdef"))
	}
	if SummaryFromConfig(nil) != nil {
		t.Error("nil config summary")
	}
	if TrimUserMessage(nil) != "" || TrimUserMessage(fmt.Errorf("  x \n")) != "x" {
		t.Error("TrimUserMessage")
	}
	if newProgressReader(strings.NewReader("x"), nil) == nil {
		t.Error("progress reader without callback")
	}
	if _, err := NewStore("/proc/nope/store"); err == nil {
		t.Error("store under /proc")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestHumanizeAllBranches(t *testing.T) {
	for err, want := range map[error]string{
		context.DeadlineExceeded:                                           "timed out",
		timeoutErr{}:                                                       "network timeout",
		fmt.Errorf("x: context deadline exceeded"):                         "while reading",
		fmt.Errorf("Client.Timeout exceeded"):                              "HTTP read timed out",
		fmt.Errorf("dial: connection refused"):                             "could not connect",
		fmt.Errorf("digest mismatch: x"):                                   "did not match",
		fmt.Errorf("manifest: 401 Unauthorized"):                           "denied access",
		fmt.Errorf("manifest unknown"):                                     "not found",
		fmt.Errorf("body was HTML/XML"):                                    "web page",
		fmt.Errorf("invalid character '<' looking for beginning of value"): "non-JSON",
		fmt.Errorf("something else"):                                       "something else",
	} {
		if got := HumanizePullError(err); !strings.Contains(got, want) {
			t.Errorf("%v: %q", err, got)
		}
	}
	if HumanizePullError(nil) != "" || HumanizeComposeBuildError(nil) != "" {
		t.Error("nil errors")
	}
	if got := HumanizeComposeBuildError(fmt.Errorf(`service "a" pull "x": manifest unknown`)); !strings.Contains(got, "not found") {
		t.Errorf("compose pull error: %q", got)
	}
	if got := HumanizeComposeBuildError(fmt.Errorf("bad yaml")); got != "bad yaml" {
		t.Errorf("compose other error: %q", got)
	}
	if got := TrimUserMessage(fmt.Errorf("%s", strings.Repeat("x", 2000))); len(got) > 1010 {
		t.Errorf("not capped: %d", len(got))
	}
}
