package discovery

import (
	"context"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// resolveWait bounds a multicast lookup. Responders answer within a second,
// and a slower answer would stall the print dialog more than it would help.
const resolveWait = 2 * time.Second

// Resolver looks up .local names over mDNS. The Go resolver only asks the
// configured DNS servers, which know nothing about printers on the link, and
// Torchio has no cgo path to nss-mdns.
type Resolver struct {
	Server *net.UDPAddr // defaults to the mDNS IPv4 group
	Wait   time.Duration
}

// IsLocal reports whether host belongs to the mDNS domain.
func IsLocal(host string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSuffix(host, ".")), ".local")
}

// Lookup returns the IPv4 addresses of a .local name. It sends from an
// ephemeral port, which RFC 6762 section 6.7 answers by unicast, so it needs
// neither the group membership nor port 5353.
func (r Resolver) Lookup(ctx context.Context, host string) ([]net.IP, error) {
	fqdn := strings.TrimSuffix(host, ".") + "."
	name, err := dnsmessage.NewName(fqdn)
	if err != nil {
		return nil, err
	}
	query, err := (&dnsmessage.Message{Questions: []dnsmessage.Question{{
		Name:  name,
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	}}}).Pack()
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	wait := r.Wait
	if wait == 0 {
		wait = resolveWait
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if r.Server != nil {
		if _, err := conn.WriteToUDP(query, r.Server); err != nil {
			return nil, err
		}
	} else {
		sendOnEveryLink(conn, query)
	}

	buf := make([]byte, 9000)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return nil, &net.DNSError{Err: "no mDNS answer", Name: host, IsNotFound: true}
		}
		if ips := addressesOf(buf[:n], fqdn); len(ips) > 0 {
			return ips, nil
		}
	}
}

// sendOnEveryLink multicasts the query on each interface that can carry it, so
// a VPN holding the default route does not swallow the lookup.
func sendOnEveryLink(conn *net.UDPConn, query []byte) {
	ifaces, err := net.Interfaces()
	if err != nil {
		_, _ = conn.WriteToUDP(query, mdnsIPv4)
		return
	}
	pc := ipv4.NewPacketConn(conn)
	for i := range ifaces {
		iface := &ifaces[i]
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if pc.SetMulticastInterface(iface) != nil {
			continue
		}
		_, _ = conn.WriteToUDP(query, mdnsIPv4)
	}
}

// addressesOf returns the A records for fqdn. Responders echo the name in their
// own case, and DNS names compare without it.
func addressesOf(packet []byte, fqdn string) []net.IP {
	var msg dnsmessage.Message
	if err := msg.Unpack(packet); err != nil {
		return nil
	}
	var ips []net.IP
	for _, sections := range [][]dnsmessage.Resource{msg.Answers, msg.Additionals} {
		for _, rr := range sections {
			a, ok := rr.Body.(*dnsmessage.AResource)
			if ok && strings.EqualFold(rr.Header.Name.String(), fqdn) {
				ips = append(ips, net.IP(a.A[:]))
			}
		}
	}
	return ips
}

// DialContext dials like net.Dialer and resolves .local names over mDNS first.
// A name the link does not answer for still reaches the system resolver, since
// some networks serve .local from DNS.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !IsLocal(host) {
		return d.DialContext(ctx, network, addr)
	}
	ips, err := Resolver{}.Lookup(ctx, host)
	if err != nil {
		return d.DialContext(ctx, network, addr)
	}
	var last error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}
