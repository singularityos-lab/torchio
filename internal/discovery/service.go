// Package discovery is Torchio's read-only network discovery. It browses for
// IPP printers via mDNS/DNS-SD (pure Go, no avahi) and records what it sees in
// the ephemeral devices_seen table. By constitution discovery creates NOTHING:
// it never adds a printer. A printer is born only when a user confirms a
// discovered device through the ush portal. This single rule eliminates the
// entire cups-browsed class of vulnerabilities (a network packet can never
// mutate the configuration).
package discovery

import (
	"net"
	"net/url"
	"strings"
)

// DNS-SD service types Torchio browses for. _universal is the AirPrint subtype.
const (
	ServiceIPP  = "_ipp._tcp.local."
	ServiceIPPS = "_ipps._tcp.local."
)

// Service is a discovered DNS-SD service instance.
type Service struct {
	Instance string            // friendly name, e.g. "Office HP"
	Type     string            // _ipp._tcp.local. or _ipps._tcp.local.
	Host     string            // SRV target, e.g. "HP1234.local."
	Port     int               // SRV port
	TXT      map[string]string // parsed TXT key=value pairs
	Addrs    []net.IP          // A/AAAA records, if seen
}

// secure reports whether the service is IPPS (encrypted).
func (s Service) secure() bool {
	return strings.HasPrefix(s.Type, "_ipps")
}

// Model returns a human label for the printer, preferring the TXT "ty"
// (make and model) field and falling back to the instance name.
func (s Service) Model() string {
	if ty := s.TXT["ty"]; ty != "" {
		return ty
	}
	return s.Instance
}

// URI builds the candidate IPP(S) URI for the service: scheme from the service
// type, host from the SRV target (or the first address), port from SRV, and
// path from the TXT "rp" (resource path) field. This is the URI a user would
// confirm through the portal; it is never used to print without that consent.
func (s Service) URI() string {
	scheme := "ipp"
	if s.secure() {
		scheme = "ipps"
	}

	host := strings.TrimSuffix(s.Host, ".")
	if host == "" && len(s.Addrs) > 0 {
		host = s.Addrs[0].String()
	}

	path := s.TXT["rp"]
	if path == "" {
		path = "ipp/print"
	}
	path = "/" + strings.TrimPrefix(path, "/")

	u := url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, itoa(s.Port)),
		Path:   path,
	}
	return u.String()
}

// Key is the stable identifier used in the devices_seen cache: the instance
// name plus its service type, so the same printer over ipp and ipps are
// distinct rows.
func (s Service) Key() string {
	return s.Instance + "|" + s.Type
}

// itoa is a tiny strconv.Itoa to avoid importing strconv for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [6]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
