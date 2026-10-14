package netdns

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

// recorder is a dns.ResponseWriter that keeps the reply.
type recorder struct{ msg *dns.Msg }

func (r *recorder) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (r *recorder) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (r *recorder) WriteMsg(m *dns.Msg) error   { r.msg = m; return nil }
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *recorder) Close() error                { return nil }
func (r *recorder) TsigStatus() error           { return nil }
func (r *recorder) TsigTimersOnly(bool)         {}
func (r *recorder) Hijack()                     {}

// A query must ask exactly one question.
func TestServeFormatError(t *testing.T) {
	e := &Embedded{}
	for _, qs := range [][]dns.Question{nil, {{Name: "a.", Qtype: dns.TypeA, Qclass: dns.ClassINET}, {Name: "b.", Qtype: dns.TypeA, Qclass: dns.ClassINET}}} {
		w := &recorder{}
		e.serve(w, &dns.Msg{Question: qs})
		if w.msg == nil || w.msg.Rcode != dns.RcodeFormatError {
			t.Errorf("%d questions: %+v", len(qs), w.msg)
		}
	}
}
