package network

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func freePort(t *testing.T, proto string) int {
	t.Helper()
	if proto == "udp" {
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).Port
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestHostBackendSamePortsNeedNothing(t *testing.T) {
	b := NewHostBackend()
	if err := b.EnsureNetwork(); err != nil {
		t.Fatal(err)
	}
	p := freePort(t, "tcp")
	ip, err := b.Setup(context.Background(), "c", "", []PortMapping{{HostPort: p, ContainerPort: p}, {HostPort: 0, ContainerPort: 9}}, nil)
	if err != nil || ip != "" || len(b.Forwarded("c")) != 0 {
		t.Fatalf("Setup = %q, %v, %v", ip, err, b.Forwarded("c"))
	}
	if err := b.Teardown(context.Background(), "c", ""); err != nil {
		t.Fatal(err)
	}
}

// The workload listens on its own port; the published host port reaches it.
func TestHostBackendForwardsTCP(t *testing.T) {
	app, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	go func() {
		for {
			c, err := app.Accept()
			if err != nil {
				return
			}
			go func() { // reads the whole request (until half-close), then answers
				defer c.Close()
				b, _ := io.ReadAll(c)
				c.Write([]byte("echo:" + string(b))) //nolint:errcheck
			}()
		}
	}()
	hostPort := freePort(t, "tcp")
	b := NewHostBackend()
	pm := PortMapping{HostPort: hostPort, ContainerPort: app.Addr().(*net.TCPAddr).Port}
	if _, err := b.Setup(context.Background(), "web", "", []PortMapping{pm}, nil); err != nil {
		t.Fatal(err)
	}
	if got := b.Forwarded("web"); len(got) != 1 || !strings.HasSuffix(got[0], "/tcp") {
		t.Errorf("Forwarded = %v", got)
	}
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(hostPort)))
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("hi"))                          //nolint:errcheck
		c.(*net.TCPConn).CloseWrite()                  //nolint:errcheck
		c.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
		got, _ := io.ReadAll(c)
		c.Close()
		if string(got) != "echo:hi" {
			t.Fatalf("round trip %d: %q", i, got)
		}
	}

	// A second container cannot take the same host port.
	if _, err := NewHostBackend().Setup(context.Background(), "other", "", []PortMapping{pm}, nil); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Errorf("port in use: %v", err)
	}
	// Setup again (a restart) rebinds; teardown frees the port.
	if _, err := b.Setup(context.Background(), "web", "", []PortMapping{pm}, nil); err != nil {
		t.Fatalf("re-setup: %v", err)
	}
	if err := b.Teardown(context.Background(), "web", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(hostPort)), time.Second); err == nil {
		t.Error("host port still open after teardown")
	}
}

func TestHostBackendForwardsUDP(t *testing.T) {
	app, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := app.ReadFrom(buf)
			if err != nil {
				return
			}
			app.WriteTo(append([]byte("pong:"), buf[:n]...), from) //nolint:errcheck
		}
	}()
	hostPort := freePort(t, "udp")
	b := NewHostBackend()
	pm := PortMapping{HostPort: hostPort, ContainerPort: app.LocalAddr().(*net.UDPAddr).Port, Protocol: "udp"}
	if _, err := b.Setup(context.Background(), "dns", "", []PortMapping{pm}, nil); err != nil {
		t.Fatal(err)
	}
	defer b.Teardown(context.Background(), "dns", "") //nolint:errcheck
	for _, msg := range []string{"a", "b"} {
		c, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", itoa(hostPort)))
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
		c.Write([]byte(msg))                           //nolint:errcheck
		buf := make([]byte, 100)
		n, err := c.Read(buf)
		c.Close()
		if err != nil || string(buf[:n]) != "pong:"+msg {
			t.Fatalf("udp %s: %q %v", msg, buf[:n], err)
		}
	}
}

func TestForwardErrors(t *testing.T) {
	for _, c := range []struct {
		p    PortMapping
		err  error
		want string
	}{
		{PortMapping{HostPort: 80, ContainerPort: 8080}, &net.OpError{Err: syscallEACCES()}, "needs root"},
		{PortMapping{HostPort: 5, ContainerPort: 6, Protocol: "udp"}, io.EOF, "publish 5:6/udp: EOF"},
	} {
		if got := forwardError(c.p, c.err).Error(); !strings.Contains(got, c.want) {
			t.Errorf("forwardError = %q, want %q", got, c.want)
		}
	}
}

func TestHostBackendSetupRollback(t *testing.T) {
	busy := freePort(t, "tcp")
	ln, err := net.Listen("tcp", "127.0.0.1:"+itoa(busy))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	okPort := freePort(t, "tcp")
	b := NewHostBackend()
	_, err = b.Setup(context.Background(), "roll", "", []PortMapping{
		{HostPort: okPort, ContainerPort: okPort + 1},
		{HostPort: busy, ContainerPort: busy + 1},
	}, nil)
	if err == nil {
		t.Fatal("expected bind failure on second forwarder")
	}
	if len(b.Forwarded("roll")) != 0 {
		t.Fatalf("rollback left forwards: %v", b.Forwarded("roll"))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func syscallEACCES() error { return syscall.EACCES }
