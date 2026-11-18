package image

import (
	"context"
	"strings"
	"testing"
)

func TestSourceAndRemoteDigest(t *testing.T) {
	reg := newFakeRegistry(t)
	ref, _ := mirrorFixture(t, reg)
	dst, _ := NewStore(t.TempDir())
	if dst.SourceDigest(ref) != "" {
		t.Fatal("digest before pull")
	}
	if _, err := dst.Pull(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	src := dst.SourceDigest(ref)
	remote, err := dst.RemoteDigest(context.Background(), ref, nil)
	if err != nil || !strings.HasPrefix(src, "sha256:") || remote != src {
		t.Fatalf("recorded %q, remote %q, err %v", src, remote, err)
	}
	// The tag moves: the remote digest changes, the recorded one does not.
	reg.mu.Lock()
	for k, v := range reg.manifests {
		reg.manifests[k] = append(append([]byte{}, v...), ' ')
	}
	reg.mu.Unlock()
	moved, err := dst.RemoteDigest(context.Background(), ref, nil)
	if err != nil || moved == src || dst.SourceDigest(ref) != src {
		t.Fatalf("moved %q vs %q: %v", moved, src, err)
	}
	// Through a mirror first, and errors.
	mirror := newFakeRegistry(t)
	dst.SetMirrors(map[string][]Mirror{reg.host(): {{URL: "http://down", Scheme: "http", Host: closedHost(t)}, mirrorOf(mirror, "")}}, false)
	if d, err := dst.RemoteDigest(context.Background(), ref, nil); err != nil || d != moved {
		t.Fatalf("fallback to the registry: %q %v", d, err)
	}
	dst.SetMirrors(map[string][]Mirror{reg.host(): {{URL: "http://down", Scheme: "http", Host: closedHost(t)}}}, true)
	if _, err := dst.RemoteDigest(context.Background(), ref, nil); err == nil || !strings.Contains(err.Error(), "every mirror failed") {
		t.Fatalf("mirror only: %v", err)
	}
	dst.SetMirrors(nil, false)
	if _, err := dst.RemoteDigest(context.Background(), reg.host()+"/team/none:1", nil); err == nil {
		t.Fatal("missing tag")
	}
	if joinRepo("p", "r") != "p/r" || joinRepo("", "r") != "r" {
		t.Fatal("joinRepo")
	}
}
