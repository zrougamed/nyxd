package image

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestParseMirror(t *testing.T) {
	for spec, want := range map[string][2]string{
		"docker.io=https://mirror.lan:5000":    {"registry-1.docker.io", "https|mirror.lan:5000|"},
		"ghcr.io=http://cache.lan/proxy/ghcr/": {"ghcr.io", "http|cache.lan|proxy/ghcr"},
		" quay.io = https://q.example.com ":    {"quay.io", "https|q.example.com|"},
	} {
		reg, m, err := ParseMirror(spec)
		got := m.Scheme + "|" + m.Host + "|" + m.Prefix
		if err != nil || reg != want[0] || got != want[1] {
			t.Errorf("%q: %s %s %v", spec, reg, got, err)
		}
	}
	for _, bad := range []string{"docker.io", "=https://x", "docker.io=", "docker.io=ftp://x", "docker.io=https://u:p@x", "docker.io=https://x?a=1", "docker.io=mirror.lan"} {
		if _, _, err := ParseMirror(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// mirrorFixture pushes one image to the registries given (upstream and/or
// mirror) under repo team/app and returns its upstream reference.
func mirrorFixture(t *testing.T, upstream *fakeRegistry, mirrors ...*fakeRegistry) (string, string) {
	t.Helper()
	src, _ := NewStore(t.TempDir())
	auth := &RegistryAuth{Username: "marwa", Password: "s3cret"}
	ref := upstream.host() + "/team/app:1.0"
	seedImage(t, src, ref)
	var digest string
	for _, r := range append([]*fakeRegistry{upstream}, mirrors...) {
		if r == nil {
			continue
		}
		mref := r.host() + "/team/app:1.0"
		if err := src.Tag(ref, mref); err != nil && mref != ref {
			t.Fatal(err)
		}
		d, err := src.Push(context.Background(), mref, auth, nil)
		if err != nil {
			t.Fatal(err)
		}
		digest = d
	}
	return ref, digest
}

func mirrorOf(r *fakeRegistry, prefix string) Mirror {
	return Mirror{URL: "http://" + r.host() + "/" + prefix, Scheme: "http", Host: r.host(), Prefix: prefix}
}

func closedHost(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := l.Addr().String()
	l.Close()
	return h
}

func TestPullThroughMirror(t *testing.T) {
	upstream, mirror := newFakeRegistry(t), newFakeRegistry(t)
	ref, _ := mirrorFixture(t, upstream, mirror)
	// Remove the image upstream: only the mirror can serve it.
	upstream.mu.Lock()
	upstream.manifests = map[string][]byte{}
	upstream.mu.Unlock()

	dst, _ := NewStore(t.TempDir())
	dst.SetMirrors(map[string][]Mirror{upstream.host(): {mirrorOf(mirror, "")}}, false)
	if _, err := dst.Pull(context.Background(), ref); err != nil {
		t.Fatalf("pull through mirror: %v", err)
	}
	// Stored under the upstream name, unchanged.
	if _, _, err := dst.LoadImageMeta(ref); err != nil {
		t.Fatalf("stored name: %v", err)
	}
	if got := dst.Mirrors()[upstream.host()]; len(got) != 1 || dst.MirrorOnly() || len(dst.MirrorHosts()) != 1 {
		t.Fatalf("mirrors: %v", got)
	}
}

func TestMirrorFallbackAndMirrorOnly(t *testing.T) {
	upstream, mirror := newFakeRegistry(t), newFakeRegistry(t)
	ref, _ := mirrorFixture(t, upstream, mirror)
	down := Mirror{URL: "http://down", Scheme: "http", Host: closedHost(t)}

	// A mirror that is down: falls back to the registry, saying so.
	dst, _ := NewStore(t.TempDir())
	dst.SetMirrors(map[string][]Mirror{upstream.host(): {down}}, false)
	var events []PullEvent
	if _, err := dst.PullWithProgress(context.Background(), ref, nil, func(e PullEvent) { events = append(events, e) }); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	found := false
	for _, e := range events {
		found = found || (e.Phase == "mirror" && strings.Contains(e.Message, "mirror http://down failed"))
	}
	if !found {
		t.Errorf("no mirror event: %+v", events)
	}

	// A mirror that serves a corrupted layer: rejected (digest check), then
	// the registry is used.
	mirror.mu.Lock()
	for d := range mirror.blobs {
		mirror.blobs[d] = []byte("tampered")
	}
	mirror.mu.Unlock()
	dst2, _ := NewStore(t.TempDir())
	dst2.SetMirrors(map[string][]Mirror{upstream.host(): {mirrorOf(mirror, "")}}, false)
	if _, err := dst2.Pull(context.Background(), ref); err != nil {
		t.Fatalf("tampered mirror, fallback: %v", err)
	}

	// -registry-mirror-only: no fallback.
	dst3, _ := NewStore(t.TempDir())
	dst3.SetMirrors(map[string][]Mirror{upstream.host(): {mirrorOf(mirror, ""), down}}, true)
	_, err := dst3.Pull(context.Background(), ref)
	if err == nil || !strings.Contains(err.Error(), "-registry-mirror-only") {
		t.Fatalf("mirror-only: %v", err)
	}
}

func TestMirrorPrefix(t *testing.T) {
	upstream, mirror := newFakeRegistry(t), newFakeRegistry(t)
	ref, _ := mirrorFixture(t, upstream)
	// The mirror serves the repository under proxy/ (a proxy project).
	src, _ := NewStore(t.TempDir())
	if _, err := src.Pull(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if err := src.Tag(ref, mirror.host()+"/proxy/team/app:1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Push(context.Background(), mirror.host()+"/proxy/team/app:1.0", &RegistryAuth{Username: "marwa", Password: "s3cret"}, nil); err != nil {
		t.Fatal(err)
	}
	upstream.mu.Lock()
	upstream.manifests = map[string][]byte{}
	upstream.mu.Unlock()
	dst, _ := NewStore(t.TempDir())
	dst.SetMirrors(map[string][]Mirror{upstream.host(): {mirrorOf(mirror, "proxy")}}, true)
	if _, err := dst.Pull(context.Background(), ref); err != nil {
		t.Fatalf("prefixed mirror: %v", err)
	}
}

// A manifest pinned by digest must hash to it, wherever it comes from.
func TestPinnedManifestDigestChecked(t *testing.T) {
	upstream := newFakeRegistry(t)
	ref, digest := mirrorFixture(t, upstream)
	repo := strings.TrimPrefix(strings.TrimSuffix(ref, ":1.0"), upstream.host()+"/")
	pinned := upstream.host() + "/" + repo + "@" + digest
	upstream.mu.Lock()
	good := upstream.manifests[repo+":1.0"]
	upstream.manifests[repo+":"+digest] = good
	upstream.mu.Unlock()
	dst, _ := NewStore(t.TempDir())
	if _, err := dst.Pull(context.Background(), pinned); err != nil {
		t.Fatalf("pinned pull: %v", err)
	}
	// Swap the manifest served under that digest.
	upstream.mu.Lock()
	upstream.manifests[repo+":"+digest] = append(append([]byte{}, good...), ' ')
	upstream.mu.Unlock()
	dst2, _ := NewStore(t.TempDir())
	if _, err := dst2.Pull(context.Background(), pinned); err == nil || !strings.Contains(err.Error(), "content has digest") {
		t.Fatalf("swapped manifest: %v", err)
	}
}

func TestMirrorAuthContext(t *testing.T) {
	ctx := WithMirrorAuth(context.Background(), AuthMap{"mirror.lan": {Username: "u", Password: "p"}, "empty": {}})
	if a := MirrorAuthFrom(ctx).host("mirror.lan"); a == nil || a.Username != "u" {
		t.Fatalf("auth: %+v", a)
	}
	if MirrorAuthFrom(ctx).host("empty") != nil || MirrorAuthFrom(context.Background()).host("x") != nil {
		t.Fatal("missing auth")
	}
	if WithMirrorAuth(context.Background(), nil) != context.Background() {
		t.Fatal("nil map changed the context")
	}
}
