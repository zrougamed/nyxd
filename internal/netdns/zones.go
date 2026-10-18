package netdns

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Zones is an embedded DNS backend with one authoritative zone (UDP/53) per
// bridge gateway. Records registered on gateway A are not visible on gateway B.
type Zones struct {
	log       *slog.Logger
	defaultGW string
	upstreams []string
	// udpPort is the listen port (default "53"). Tests may set a high port
	// so coverage does not require CAP_NET_BIND_SERVICE.
	udpPort string

	mu    sync.Mutex
	zones map[string]*zone // gateway IPv4 string → zone
}

type zone struct {
	gw      net.IP
	records map[string]net.IP
	server  *dns.Server
	started bool
	err     error
}

// NewZones constructs a multi-gateway embedded resolver. defaultGW is used by
// Register/Deregister when no compose bridge is selected.
func NewZones(log *slog.Logger, defaultGW net.IP) *Zones {
	if log == nil {
		log = slog.Default()
	}
	gw := ""
	if defaultGW != nil {
		if v4 := defaultGW.To4(); v4 != nil {
			gw = v4.String()
		}
	}
	up := hostUpstreamResolvers()
	log.Info("embedded dns upstreams", "addrs", up)
	return &Zones{
		log:       log,
		defaultGW: gw,
		upstreams: up,
		udpPort:   "53",
		zones:     make(map[string]*zone),
	}
}

// SetUDPPort overrides the DNS listen port (tests only; production stays on 53).
func (z *Zones) SetUDPPort(port string) {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "53"
	}
	z.mu.Lock()
	z.udpPort = port
	z.mu.Unlock()
}

// Register publishes host on the default gateway zone.
func (z *Zones) Register(host, ip string) error {
	return z.RegisterOn(z.defaultGW, host, ip)
}

// Deregister removes host from the default gateway zone.
func (z *Zones) Deregister(host string) error {
	return z.DeregisterOn(z.defaultGW, host)
}

// RegisterOn publishes host in the zone listening on gateway.
func (z *Zones) RegisterOn(gateway, host, ipStr string) error {
	gw, err := parseGateway(gateway)
	if err != nil {
		return err
	}
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("embedded dns: invalid ipv4 %q", ipStr)
	}
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return fmt.Errorf("embedded dns: empty host")
	}
	if !isDNSLabel(host) {
		return fmt.Errorf("embedded dns: invalid host label %q", host)
	}

	z.mu.Lock()
	defer z.mu.Unlock()
	zn, err := z.ensureZoneLocked(gw)
	if err != nil {
		return err
	}
	zn.records[dns.Fqdn(host)] = ip4
	zn.records[dns.Fqdn(host+".nyxd.local")] = ip4
	return nil
}

// DeregisterOn removes host from the zone for gateway.
func (z *Zones) DeregisterOn(gateway, host string) error {
	gw, err := parseGateway(gateway)
	if err != nil {
		return nil
	}
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return nil
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	zn := z.zones[gw.String()]
	if zn == nil {
		return nil
	}
	delete(zn.records, dns.Fqdn(host))
	delete(zn.records, dns.Fqdn(host+".nyxd.local"))
	return nil
}

// Shutdown stops every gateway listener.
func (z *Zones) Shutdown() {
	z.mu.Lock()
	defer z.mu.Unlock()
	for _, zn := range z.zones {
		if zn.server != nil {
			if err := zn.server.Shutdown(); err != nil {
				z.log.Warn("embedded dns shutdown", "gw", zn.gw, "err", err)
			}
			zn.server = nil
		}
	}
}

func (z *Zones) ensureZoneLocked(gw net.IP) (*zone, error) {
	key := gw.String()
	if zn, ok := z.zones[key]; ok {
		if zn.err != nil {
			return nil, zn.err
		}
		return zn, nil
	}
	zn := &zone{
		gw:      gw,
		records: make(map[string]net.IP),
	}
	z.zones[key] = zn
	zn.err = z.startZone(zn)
	if zn.err != nil {
		return nil, zn.err
	}
	return zn, nil
}

func (z *Zones) startZone(zn *zone) error {
	if zn.started {
		return zn.err
	}
	port := z.udpPort
	if port == "" {
		port = "53"
	}
	addr := net.JoinHostPort(zn.gw.String(), port)
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("embedded dns listen %s: %w", addr, err)
	}
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		z.serveZone(zn, w, r)
	})
	srv := &dns.Server{PacketConn: pc, Handler: handler}
	zn.server = srv
	zn.started = true
	go func() {
		if err := srv.ActivateAndServe(); err != nil {
			z.log.Error("embedded dns server exited", "addr", addr, "err", err)
		}
	}()
	z.log.Info("embedded dns listening", "addr", addr)
	return nil
}

func (z *Zones) serveZone(zn *zone, w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	if len(r.Question) != 1 {
		m.Rcode = dns.RcodeFormatError
		_ = w.WriteMsg(m)
		return
	}
	q := r.Question[0]
	qname := strings.ToLower(q.Name)
	z.mu.Lock()
	ip, local := zn.records[qname]
	z.mu.Unlock()
	if local {
		if q.Qtype != dns.TypeA {
			m.Rcode = dns.RcodeSuccess
			_ = w.WriteMsg(m)
			return
		}
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
			A:   ip,
		})
		_ = w.WriteMsg(m)
		return
	}
	if resp := z.forward(r); resp != nil {
		_ = w.WriteMsg(resp)
		return
	}
	m.SetReply(r)
	m.Authoritative = true
	m.Rcode = dns.RcodeServerFailure
	_ = w.WriteMsg(m)
}

func (z *Zones) forward(r *dns.Msg) *dns.Msg {
	if len(z.upstreams) == 0 {
		return nil
	}
	c := &dns.Client{Net: "udp", Timeout: 4 * time.Second}
	req := r.Copy()
	for _, addr := range z.upstreams {
		in, _, err := c.Exchange(req, addr)
		if err != nil {
			z.log.Debug("embedded dns forward", "upstream", addr, "err", err)
			continue
		}
		if in != nil {
			return in
		}
	}
	return nil
}

func parseGateway(gateway string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(gateway))
	if ip4 := ip.To4(); ip4 != nil {
		return ip4, nil
	}
	return nil, fmt.Errorf("embedded dns: invalid gateway %q", gateway)
}

var _ Backend = (*Zones)(nil)
