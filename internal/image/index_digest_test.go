package image

import (
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestPickIndexDigest(t *testing.T) {
	manifests := []oci.Descriptor{
		{Digest: "sha256:aaa", Platform: &oci.Platform{OS: "linux", Architecture: "amd64"}},
		{Digest: "sha256:bbb", Platform: &oci.Platform{OS: "linux", Architecture: "arm64"}},
	}
	d, err := pickIndexDigest(manifests, "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if d != "sha256:bbb" {
		t.Fatalf("got %q", d)
	}
	_, err = pickIndexDigest(manifests, "linux", "riscv64")
	if err == nil {
		t.Fatal("expected error")
	}
}
