package share

import (
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func ptrQuery(t *testing.T, name string) []byte {
	t.Helper()
	msg := dnsmessage.Message{
		Questions: []dnsmessage.Question{{
			Name:  dnsmessage.MustNewName(name),
			Type:  dnsmessage.TypePTR,
			Class: dnsmessage.ClassINET,
		}},
	}
	b, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sampleService() ServiceInfo {
	return ServiceInfo{
		Instance: "Office Printer",
		Type:     "_ipps._tcp.local.",
		Host:     "torchio.local.",
		Port:     8443,
		TXT:      map[string]string{"rp": "ipp/print", "ty": "Office Printer"},
		Addrs:    []net.IP{net.IPv4(127, 0, 0, 1)},
	}
}

func TestResponderAnswersMatchingQuery(t *testing.T) {
	r := NewResponder(sampleService())
	resp, ok := r.answer(ptrQuery(t, "_ipps._tcp.local."))
	if !ok {
		t.Fatal("responder should answer a query for its service type")
	}

	// Parse the response and check the records.
	var (
		ptr  string
		port uint16
		txt  []string
		addr net.IP
	)
	var p dnsmessage.Parser
	if _, err := p.Start(resp); err != nil {
		t.Fatal(err)
	}
	p.SkipAllQuestions()
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			break
		}
		switch h.Type {
		case dnsmessage.TypePTR:
			r, _ := p.PTRResource()
			ptr = r.PTR.String()
		case dnsmessage.TypeSRV:
			r, _ := p.SRVResource()
			port = r.Port
		case dnsmessage.TypeTXT:
			r, _ := p.TXTResource()
			txt = r.TXT
		case dnsmessage.TypeA:
			r, _ := p.AResource()
			addr = net.IP(r.A[:])
		default:
			p.SkipAnswer()
		}
	}

	if ptr != "Office Printer._ipps._tcp.local." {
		t.Errorf("PTR = %q", ptr)
	}
	if port != 8443 {
		t.Errorf("SRV port = %d, want 8443", port)
	}
	if !contains(txt, "rp=ipp/print") {
		t.Errorf("TXT = %v, want rp=ipp/print", txt)
	}
	if !addr.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("A = %v", addr)
	}
}

func TestResponderIgnoresOtherServices(t *testing.T) {
	r := NewResponder(sampleService())
	if _, ok := r.answer(ptrQuery(t, "_printer._tcp.local.")); ok {
		t.Error("responder should not answer for a service it does not advertise")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
