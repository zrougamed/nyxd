package supervisor

import (
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestComposeSpecsEqual(t *testing.T) {
	base := ContainerSpec{
		ID:    "proj-web",
		Image: "nginx:alpine",
		Env:   []string{"A=1", "B=2"},
		Args:  []string{"/bin/sh", "-c", "nginx"},
		ManifestLayers: []oci.Descriptor{
			{Digest: "sha256:aaa", MediaType: oci.MediaTypeImageLayerGzip},
		},
		Hostname: "web",
	}
	a := base
	b := base
	b.Env = []string{"B=2", "A=1"} // order differs
	if !composeSpecsEqual(a, b) {
		t.Fatal("expected equal after env sort")
	}
	b2 := base
	b2.Env = []string{"A=2", "B=2"}
	if composeSpecsEqual(a, b2) {
		t.Fatal("expected unequal env value")
	}
	b3 := base
	b3.ManifestLayers = []oci.Descriptor{{Digest: "sha256:bbb", MediaType: oci.MediaTypeImageLayerGzip}}
	if composeSpecsEqual(a, b3) {
		t.Fatal("expected unequal layers")
	}
}
