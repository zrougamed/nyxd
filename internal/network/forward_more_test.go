package network

import (
	"io"
	"net"
	"testing"
	"time"
)

func freeUDPPort(t *testing.T) (int, net.PacketConn) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return pc.LocalAddr().(*net.UDPAddr).Port, pc
}

func TestForwarderEdges(t *testing.T) {
	// A UDP port already taken.
	port, pc := freeUDPPort(t)
	busy, err := net.ListenPacket("udp", net.JoinHostPort("", itoa(port)))
	pc.Close()
	if err == nil {
		defer busy.Close()
		if _, err := startForwarder(PortMapping{HostPort: port, ContainerPort: 1, Protocol: "udp"}); err == nil {
			t.Error("UDP forwarder on a busy port")
		}
	}

	// Nothing listens on the container side: the client is hung up on.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	hostPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadPort := dead.Addr().(*net.TCPAddr).Port
	dead.Close()
	f, err := startForwarder(PortMapping{HostPort: hostPort, ContainerPort: deadPort})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(hostPort)))
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	if n, _ := c.Read(make([]byte, 1)); n != 0 {
		t.Error("data from nowhere")
	}
	c.Close()

	// A tracked connection is closed with the forwarder; after close nothing
	// new is tracked, and closing twice is harmless.
	a, b := net.Pipe()
	defer b.Close()
	if !f.track(a, true) {
		t.Fatal("track before close")
	}
	f.close()
	if _, err := a.Write([]byte("x")); err == nil {
		t.Error("tracked connection still open after close")
	}
	if f.track(b, true) {
		t.Error("tracked after close")
	}
	f.close()
}

// pipe closes a connection that cannot half-close.
func TestPipeWithoutHalfClose(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()
	done := make(chan struct{})
	go func() { pipe(a2, b1); close(done) }()
	go func() { a1.Write([]byte("hi")); a1.Close() }() //nolint:errcheck
	got, _ := io.ReadAll(b2)
	b2.Close()
	<-done
	if string(got) != "hi" {
		t.Errorf("piped %q", got)
	}
}
