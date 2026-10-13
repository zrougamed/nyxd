package network

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// udpIdle closes a UDP flow (one client address) after this long without traffic.
var udpIdle = 2 * time.Minute

// forwarder relays one published host port to the container port on the
// loopback (the container shares the host network).
type forwarder struct {
	pm     PortMapping
	ln     net.Listener   // tcp
	pc     net.PacketConn // udp
	target string
	wg     sync.WaitGroup

	mu    sync.Mutex
	conns map[net.Conn]struct{}
	done  chan struct{}
}

func (f *forwarder) proto() string {
	if strings.EqualFold(f.pm.Protocol, "udp") {
		return "udp"
	}
	return "tcp"
}

func startForwarder(pm PortMapping) (*forwarder, error) {
	f := &forwarder{
		pm:     pm,
		target: net.JoinHostPort("127.0.0.1", fmt.Sprint(pm.ContainerPort)),
		conns:  map[net.Conn]struct{}{},
		done:   make(chan struct{}),
	}
	addr := fmt.Sprintf(":%d", pm.HostPort)
	var err error
	if f.proto() == "udp" {
		if f.pc, err = net.ListenPacket("udp", addr); err != nil {
			return nil, err
		}
		f.wg.Add(1)
		go f.serveUDP()
		return f, nil
	}
	if f.ln, err = net.Listen("tcp", addr); err != nil {
		return nil, err
	}
	f.wg.Add(1)
	go f.serveTCP()
	return f, nil
}

func (f *forwarder) track(c net.Conn, add bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if add {
		select {
		case <-f.done:
			return false
		default:
		}
		f.conns[c] = struct{}{}
	} else {
		delete(f.conns, c)
	}
	return true
}

func (f *forwarder) close() {
	f.mu.Lock()
	select {
	case <-f.done:
		f.mu.Unlock()
		return
	default:
	}
	close(f.done)
	for c := range f.conns {
		c.Close()
	}
	f.mu.Unlock()
	if f.ln != nil {
		f.ln.Close()
	}
	if f.pc != nil {
		f.pc.Close()
	}
	f.wg.Wait()
}

func (f *forwarder) serveTCP() {
	defer f.wg.Done()
	for {
		in, err := f.ln.Accept()
		if err != nil {
			return // closed
		}
		if !f.track(in, true) {
			in.Close()
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer f.track(in, false)
			defer in.Close()
			out, err := net.DialTimeout("tcp", f.target, 5*time.Second)
			if err != nil {
				return // nothing listening in the container (yet)
			}
			if !f.track(out, true) {
				out.Close()
				return
			}
			defer f.track(out, false)
			defer out.Close()
			pipe(in, out)
		}()
	}
}

// pipe copies both ways, passing a half-close on so request/response
// protocols that shut down their write side still work.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src) //nolint:errcheck
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite() //nolint:errcheck
		} else {
			dst.Close()
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

func (f *forwarder) serveUDP() {
	defer f.wg.Done()
	type flow struct {
		conn net.Conn
		last time.Time
	}
	var mu sync.Mutex
	flows := map[string]*flow{}
	defer func() {
		mu.Lock()
		for _, fl := range flows {
			fl.conn.Close()
		}
		mu.Unlock()
	}()
	buf := make([]byte, 64*1024)
	for {
		n, src, err := f.pc.ReadFrom(buf)
		if err != nil {
			return // closed
		}
		key := src.String()
		mu.Lock()
		fl, ok := flows[key]
		if !ok {
			c, err := net.Dial("udp", f.target)
			if err != nil {
				mu.Unlock()
				continue
			}
			fl = &flow{conn: c}
			flows[key] = fl
			f.wg.Add(1)
			go func(src net.Addr, fl *flow) { // replies back to the client
				defer f.wg.Done()
				rb := make([]byte, 64*1024)
				for {
					fl.conn.SetReadDeadline(time.Now().Add(udpIdle)) //nolint:errcheck
					m, err := fl.conn.Read(rb)
					if err == nil {
						f.pc.WriteTo(rb[:m], src) //nolint:errcheck
						continue
					}
					mu.Lock()
					idle := time.Since(fl.last) >= udpIdle
					ne, timeout := err.(net.Error)
					if timeout && ne.Timeout() && !idle {
						mu.Unlock()
						continue // the client sent recently: keep the flow
					}
					if flows[src.String()] == fl {
						delete(flows, src.String())
					}
					mu.Unlock()
					fl.conn.Close()
					return
				}
			}(src, fl)
		}
		fl.last = time.Now()
		c := fl.conn
		mu.Unlock()
		c.Write(buf[:n]) //nolint:errcheck
	}
}
