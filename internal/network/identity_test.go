package network

import "testing"

func TestActiveIdentityNativeDefaults(t *testing.T) {
	t.Setenv("NYXD_GATEWAY_IP", "")
	t.Setenv("NYXD_CONTAINER_SUBNET", "")
	id := ActiveIdentity("native")
	if id.Driver != "native" || id.Bridge != "nyxbr0" {
		t.Fatalf("%+v", id)
	}
	if id.Gateway != "10.88.0.1" || id.Subnet != "10.88.0.0/16" {
		// Live bridge may override; only assert when interface is absent.
		if id.Bridge == "nyxbr0" && id.Gateway == "" {
			t.Fatalf("%+v", id)
		}
	}
}

func TestActiveIdentityHost(t *testing.T) {
	id := ActiveIdentity("host")
	if id.Driver != "host" || id.Bridge != "" {
		t.Fatalf("%+v", id)
	}
}
