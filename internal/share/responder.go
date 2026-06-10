package share

import (
	"context"
	"net"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// mdnsAddr is the IPv4 mDNS multicast endpoint.
var mdnsAddr = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// ServiceInfo is one DNS-SD service the responder advertises (the shared
// printer). The browser side lives in internal/discovery; this is its mirror:
// the read-only browser never answers, the responder only answers for services
// the user has explicitly chosen to share.
type ServiceInfo struct {
	Instance string            // friendly name, e.g. "Office Printer"
	Type     string            // e.g. "_ipps._tcp.local."
	Host     string            // e.g. "torchio.local."
	Port     int               // SRV port
	TXT      map[string]string // rp, ty, ...
	Addrs    []net.IP          // A/AAAA addresses
}

func (s ServiceInfo) instanceFQDN() string { return s.Instance + "." + s.Type }

// Responder answers mDNS queries for the shared services and announces them.
type Responder struct {
	services []ServiceInfo
}

// NewResponder builds a responder for the given services.
func NewResponder(services ...ServiceInfo) *Responder {
	return &Responder{services: services}
}

// answer parses a query and, if it asks for one of our services (by service
// type or by instance), returns an mDNS response packet announcing that
// service. The boolean reports whether anything matched.
func (r *Responder) answer(query []byte) ([]byte, bool) {
	var p dnsmessage.Parser
	if _, err := p.Start(query); err != nil {
		return nil, false
	}
	qs, err := p.AllQuestions()
	if err != nil {
		return nil, false
	}

	matched := map[int]bool{}
	for _, q := range qs {
		name := q.Name.String()
		for i, svc := range r.services {
			if name == svc.Type || name == svc.instanceFQDN() {
				matched[i] = true
			}
		}
	}
	if len(matched) == 0 {
		return nil, false
	}

	var svcs []ServiceInfo
	for i := range r.services {
		if matched[i] {
			svcs = append(svcs, r.services[i])
		}
	}
	pkt, err := buildResponse(svcs)
	if err != nil {
		return nil, false
	}
	return pkt, true
}

// announcement builds the unsolicited (gratuitous) response that advertises all
// services, sent on startup and periodically.
func (r *Responder) announcement() ([]byte, error) {
	return buildResponse(r.services)
}

// buildResponse packs PTR/SRV/TXT/A records for the given services into an mDNS
// response message.
func buildResponse(services []ServiceInfo) ([]byte, error) {
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{Response: true, Authoritative: true},
	}
	const ttl = 120
	for _, svc := range services {
		serviceType := dnsmessage.MustNewName(svc.Type)
		instance := dnsmessage.MustNewName(svc.instanceFQDN())
		host := dnsmessage.MustNewName(svc.Host)

		// PTR: service type -> instance.
		msg.Answers = append(msg.Answers, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: serviceType, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET, TTL: ttl},
			Body:   &dnsmessage.PTRResource{PTR: instance},
		})
		// SRV: instance -> host:port.
		msg.Answers = append(msg.Answers, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: instance, Type: dnsmessage.TypeSRV, Class: dnsmessage.ClassINET, TTL: ttl},
			Body:   &dnsmessage.SRVResource{Target: host, Port: uint16(svc.Port)},
		})
		// TXT: instance attributes.
		msg.Answers = append(msg.Answers, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: instance, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: ttl},
			Body:   &dnsmessage.TXTResource{TXT: txtPairs(svc.TXT)},
		})
		// A: host -> address(es).
		for _, ip := range svc.Addrs {
			if v4 := ip.To4(); v4 != nil {
				var a [4]byte
				copy(a[:], v4)
				msg.Answers = append(msg.Answers, dnsmessage.Resource{
					Header: dnsmessage.ResourceHeader{Name: host, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: ttl},
					Body:   &dnsmessage.AResource{A: a},
				})
			}
		}
	}
	return msg.Pack()
}

// txtPairs renders a TXT map as "key=value" strings in a stable-enough order.
func txtPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// Run listens for mDNS queries and answers them, and re-announces the services
// periodically, until ctx is cancelled. A failure to open the multicast socket
// is returned (the caller treats it as non-fatal).
func (r *Responder) Run(ctx context.Context) error {
	conn, err := net.ListenMulticastUDP("udp4", nil, mdnsAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now())
	}()

	// Announce immediately and on an interval.
	go r.announceLoop(ctx, conn)

	buf := make([]byte, 9000)
	for {
		if ctx.Err() != nil {
			return nil
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		if resp, ok := r.answer(buf[:n]); ok {
			_, _ = conn.WriteToUDP(resp, mdnsAddr)
		}
	}
}

func (r *Responder) announceLoop(ctx context.Context, conn *net.UDPConn) {
	send := func() {
		if pkt, err := r.announcement(); err == nil {
			_, _ = conn.WriteToUDP(pkt, mdnsAddr)
		}
	}
	send()
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			send()
		}
	}
}
