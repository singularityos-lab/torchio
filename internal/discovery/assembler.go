package discovery

import (
	"net"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// assembler accumulates DNS-SD records across mDNS responses and assembles them
// into Service values. A real printer answers with PTR/SRV/TXT/A records spread
// across one or more packets; the assembler joins them by the instance and host
// names. It is a pure data structure, which makes the parsing the unit-testable
// heart of discovery (no socket required).
type assembler struct {
	want        map[string]bool     // service types of interest
	byInstance  map[string]*Service // keyed by the instance FQDN
	addrsByHost map[string][]net.IP // host FQDN -> addresses
}

func newAssembler(types ...string) *assembler {
	a := &assembler{
		want:        make(map[string]bool, len(types)),
		byInstance:  make(map[string]*Service),
		addrsByHost: make(map[string][]net.IP),
	}
	for _, t := range types {
		a.want[t] = true
	}
	return a
}

// ingest parses one mDNS message and returns the services that currently have
// enough data to be usable (an SRV port). The same service may be returned on
// successive ingests as more records arrive; callers upsert, so that is fine.
func (a *assembler) ingest(data []byte) []Service {
	var p dnsmessage.Parser
	if _, err := p.Start(data); err != nil {
		return nil
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil
	}
	a.readSection(&p, p.AnswerHeader, p.SkipAnswer)
	_ = p.SkipAllAuthorities()
	a.readSection(&p, p.AdditionalHeader, p.SkipAdditional)
	return a.ready()
}

// readSection walks one resource section, dispatching the record types we care
// about and skipping the rest. The typed body readers are section-agnostic; the
// header and skip functions are section-specific and passed in.
func (a *assembler) readSection(p *dnsmessage.Parser, header func() (dnsmessage.ResourceHeader, error), skip func() error) {
	for {
		h, err := header()
		if err != nil {
			return // ErrSectionDone or a parse error: stop this section
		}
		switch h.Type {
		case dnsmessage.TypePTR:
			if r, err := p.PTRResource(); err == nil {
				a.onPTR(h.Name.String(), r.PTR.String())
			}
		case dnsmessage.TypeSRV:
			if r, err := p.SRVResource(); err == nil {
				a.onSRV(h.Name.String(), r.Target.String(), int(r.Port))
			}
		case dnsmessage.TypeTXT:
			if r, err := p.TXTResource(); err == nil {
				a.onTXT(h.Name.String(), r.TXT)
			}
		case dnsmessage.TypeA:
			if r, err := p.AResource(); err == nil {
				a.onAddr(h.Name.String(), net.IP(r.A[:]))
			}
		case dnsmessage.TypeAAAA:
			if r, err := p.AAAAResource(); err == nil {
				a.onAddr(h.Name.String(), net.IP(r.AAAA[:]))
			}
		default:
			if err := skip(); err != nil {
				return
			}
		}
	}
}

// onPTR notes that an instance exists for a service type.
func (a *assembler) onPTR(serviceType, instanceFQDN string) {
	if !a.want[serviceType] {
		return
	}
	a.ensure(instanceFQDN)
}

// onSRV records the host and port for an instance.
func (a *assembler) onSRV(instanceFQDN, target string, port int) {
	s := a.ensure(instanceFQDN)
	if s == nil {
		return
	}
	s.Host = target
	s.Port = port
}

// onTXT records the TXT key=value pairs for an instance.
func (a *assembler) onTXT(instanceFQDN string, txt []string) {
	s := a.ensure(instanceFQDN)
	if s == nil {
		return
	}
	for _, kv := range txt {
		k, v, _ := strings.Cut(kv, "=")
		if k != "" {
			s.TXT[k] = v
		}
	}
}

// onAddr records an address for a host.
func (a *assembler) onAddr(hostFQDN string, ip net.IP) {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	a.addrsByHost[hostFQDN] = append(a.addrsByHost[hostFQDN], dup)
}

// ensure returns (creating if needed) the Service for an instance FQDN whose
// service type is one we want. Returns nil if the FQDN is not a wanted type.
func (a *assembler) ensure(instanceFQDN string) *Service {
	if s, ok := a.byInstance[instanceFQDN]; ok {
		return s
	}
	for t := range a.want {
		if strings.HasSuffix(instanceFQDN, t) && len(instanceFQDN) > len(t) {
			instance := strings.TrimSuffix(instanceFQDN, t)
			instance = strings.TrimSuffix(instance, ".")
			s := &Service{
				Instance: unescapeLabel(instance),
				Type:     t,
				TXT:      make(map[string]string),
			}
			a.byInstance[instanceFQDN] = s
			return s
		}
	}
	return nil
}

// ready returns every assembled service that has an SRV port, with addresses
// attached from the host map.
func (a *assembler) ready() []Service {
	var out []Service
	for _, s := range a.byInstance {
		if s.Port == 0 {
			continue
		}
		cp := *s
		cp.Addrs = a.addrsByHost[s.Host]
		out = append(out, cp)
	}
	return out
}

// unescapeLabel decodes the DNS-SD \DDD decimal escapes that dnsmessage emits
// for bytes like spaces, so "Office\032HP" becomes "Office HP". Other backslash
// escapes (e.g. "\.") drop the backslash.
func unescapeLabel(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			n := int(s[i+1]-'0')*100 + int(s[i+2]-'0')*10 + int(s[i+3]-'0')
			b.WriteByte(byte(n))
			i += 4
			continue
		}
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
