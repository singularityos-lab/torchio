package discovery

import (
	"context"
	"net"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// mDNS multicast endpoints (IPv4 and IPv6 link-local).
var (
	mdnsIPv4 = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	mdnsIPv6 = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353}
)

// MDNS is the pure-Go mDNS/DNS-SD browser. It joins the multicast group, sends
// periodic PTR queries for the IPP service types, and feeds every response into
// an assembler. It needs no avahi and no cgo. It is strictly a reader of the
// network: it answers nothing and changes no state but the in-memory assembler.
type MDNS struct {
	Types      []string      // service types to browse (defaults to IPP+IPPS)
	QueryEvery time.Duration // re-query interval (defaults to 30s)
}

// Browse starts browsing and returns a channel of discovered services.
func (m MDNS) Browse(ctx context.Context) (<-chan Service, error) {
	types := m.Types
	if len(types) == 0 {
		types = []string{ServiceIPP, ServiceIPPS}
	}
	every := m.QueryEvery
	if every == 0 {
		every = 30 * time.Second
	}

	// Listen for multicast responses. ListenMulticastUDP joins the group on a
	// suitable interface and returns a conn we can also send from.
	conn, err := net.ListenMulticastUDP("udp4", nil, mdnsIPv4)
	if err != nil {
		return nil, err
	}

	out := make(chan Service)
	go func() {
		defer close(out)
		defer conn.Close()

		asm := newAssembler(types...)

		// Sender: query immediately and then on the interval.
		go m.queryLoop(ctx, conn, types, every)

		// Reader: parse responses until the context ends.
		go func() {
			<-ctx.Done()
			_ = conn.SetReadDeadline(time.Now())
		}()

		buf := make([]byte, 9000) // jumbo, mDNS packets can be large
		for {
			if ctx.Err() != nil {
				return
			}
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			for _, s := range asm.changed(asm.ingest(buf[:n]), time.Now()) {
				select {
				case out <- s:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// queryLoop sends DNS-SD PTR queries for the service types on a timer.
func (m MDNS) queryLoop(ctx context.Context, conn *net.UDPConn, types []string, every time.Duration) {
	send := func() {
		for _, t := range types {
			if q, err := buildQuery(t); err == nil {
				_, _ = conn.WriteToUDP(q, mdnsIPv4)
				_, _ = conn.WriteToUDP(q, mdnsIPv6)
			}
		}
	}
	send()
	t := time.NewTicker(every)
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

// buildQuery encodes an mDNS PTR question for a service type.
func buildQuery(serviceType string) ([]byte, error) {
	name, err := dnsmessage.NewName(serviceType)
	if err != nil {
		return nil, err
	}
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{RecursionDesired: false},
		Questions: []dnsmessage.Question{{
			Name:  name,
			Type:  dnsmessage.TypePTR,
			Class: dnsmessage.ClassINET,
		}},
	}
	return msg.Pack()
}
