package supervisor

import (
	"strings"
	"testing"

	"github.com/zrougamed/nyxd/internal/network"
)

func TestCheckHostPorts(t *testing.T) {
	old := unprivilegedPortStart
	unprivilegedPortStart = func() int { return 1024 }
	t.Cleanup(func() { unprivilegedPortStart = old })

	ports := []network.PortMapping{{HostPort: 80, ContainerPort: 80}, {HostPort: 8443, ContainerPort: 443}, {HostPort: 81, ContainerPort: 80, Protocol: "udp"}, {HostPort: 8080, ContainerPort: 8080}}
	s := &Supervisor{netDriver: "host", rootless: true}
	err := s.checkHostPorts(ContainerSpec{PortMappings: ports})
	if err == nil {
		t.Fatal("low ports accepted rootless")
	}
	for _, want := range []string{"ports 80, 81, 443 cannot be used rootless", "ip_unprivileged_port_start=80", ">= 1024"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%v", want, err)
		}
	}
	if err := s.checkHostPorts(ContainerSpec{PortMappings: []network.PortMapping{{HostPort: 80, ContainerPort: 8080}}}); err == nil || !strings.Contains(err.Error(), "port 80 ") {
		t.Errorf("low host port forwarded to a high one: %v", err)
	}
	if err := s.checkHostPorts(ContainerSpec{PortMappings: ports[3:]}); err != nil {
		t.Errorf("high port: %v", err)
	}
	if err := (&Supervisor{netDriver: "host"}).checkHostPorts(ContainerSpec{PortMappings: ports}); err != nil {
		t.Errorf("rootful host network: %v", err)
	}
	if err := (&Supervisor{netDriver: "native", rootless: true}).checkHostPorts(ContainerSpec{PortMappings: ports}); err != nil {
		t.Errorf("own network namespace: %v", err)
	}
	unprivilegedPortStart = func() int { return 0 }
	if err := s.checkHostPorts(ContainerSpec{PortMappings: ports}); err != nil {
		t.Errorf("sysctl lowered: %v", err)
	}
	if n := old(); n < 0 || n > 65536 {
		t.Errorf("sysctl read %d", n)
	}
}
