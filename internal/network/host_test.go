package network

import (
	"context"
	"testing"
)

func TestHostBackendNoop(t *testing.T) {
	var b Backend = HostBackend{}
	if err := b.EnsureNetwork(); err != nil {
		t.Fatal(err)
	}
	ip, err := b.Setup(context.Background(), "c", "", []PortMapping{{HostPort: 8080, ContainerPort: 80}}, nil)
	if err != nil || ip != "" {
		t.Fatalf("Setup = %q, %v", ip, err)
	}
	if err := b.Teardown(context.Background(), "c", ""); err != nil {
		t.Fatal(err)
	}
}
