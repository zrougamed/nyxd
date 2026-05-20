package supervisor

import (
	"encoding/json"
	"testing"

	"github.com/zrougamed/nyxd/pkg/oci"
)

func TestContainerSpecJSONRoundTrip(t *testing.T) {
	spec := ContainerSpec{
		ID:    "test-1",
		Image: "nginx:1.29",
		ImageConfig: &oci.ImageConfig{
			Architecture: "amd64",
			OS:           "linux",
			Config: oci.ContainerConfig{
				ExposedPorts: map[string]struct{}{"80/tcp": {}},
			},
		},
		EmbedDNS: true,
	}
	rec := containerPersistRecord{Version: supervisorPersistVersion, Spec: spec}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out containerPersistRecord
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Spec.Image != spec.Image {
		t.Fatalf("image mismatch")
	}
	if !out.Spec.EmbedDNS {
		t.Fatalf("embed_dns mismatch")
	}
}
