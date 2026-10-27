package discovery

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/singularityos-lab/torchio/internal/store"
)

// buildResponse crafts a realistic mDNS response: the PTR in the answer
// section, and SRV/TXT/A in the additional section (as real responders do).
func buildResponse(t *testing.T) []byte {
	t.Helper()
	name := func(s string) dnsmessage.Name { return dnsmessage.MustNewName(s) }

	msg := dnsmessage.Message{
		Header: dnsmessage.Header{Response: true, Authoritative: true},
		Answers: []dnsmessage.Resource{
			{
				Header: dnsmessage.ResourceHeader{Name: name("_ipp._tcp.local."), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET},
				Body:   &dnsmessage.PTRResource{PTR: name("Office\\032HP._ipp._tcp.local.")},
			},
		},
		Additionals: []dnsmessage.Resource{
			{
				Header: dnsmessage.ResourceHeader{Name: name("Office\\032HP._ipp._tcp.local."), Type: dnsmessage.TypeSRV, Class: dnsmessage.ClassINET},
				Body:   &dnsmessage.SRVResource{Priority: 0, Weight: 0, Port: 631, Target: name("HP1234.local.")},
			},
			{
				Header: dnsmessage.ResourceHeader{Name: name("Office\\032HP._ipp._tcp.local."), Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET},
				Body:   &dnsmessage.TXTResource{TXT: []string{"rp=ipp/print", "ty=HP LaserJet 400", "note=front desk"}},
			},
			{
				Header: dnsmessage.ResourceHeader{Name: name("HP1234.local."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
				Body:   &dnsmessage.AResource{A: [4]byte{192, 168, 1, 50}},
			},
		},
	}
	packed, err := msg.Pack()
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return packed
}

func TestAssemblerSingleMessage(t *testing.T) {
	asm := newAssembler(ServiceIPP, ServiceIPPS)
	services := asm.ingest(buildResponse(t))
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d: %+v", len(services), services)
	}
	s := services[0]
	if s.Instance != "Office HP" {
		t.Errorf("instance = %q, want %q (escape not decoded?)", s.Instance, "Office HP")
	}
	if s.Type != ServiceIPP {
		t.Errorf("type = %q", s.Type)
	}
	if s.Host != "HP1234.local." || s.Port != 631 {
		t.Errorf("host/port = %q/%d", s.Host, s.Port)
	}
	if s.TXT["rp"] != "ipp/print" || s.TXT["ty"] != "HP LaserJet 400" {
		t.Errorf("TXT = %+v", s.TXT)
	}
	if len(s.Addrs) != 1 || !s.Addrs[0].Equal(net.ParseIP("192.168.1.50")) {
		t.Errorf("addrs = %v", s.Addrs)
	}
	if got := s.URI(); got != "ipp://HP1234.local:631/ipp/print" {
		t.Errorf("URI = %q", got)
	}
	if got := s.Model(); got != "HP LaserJet 400" {
		t.Errorf("Model = %q", got)
	}
}

func TestAssemblerIgnoresUnwantedTypes(t *testing.T) {
	// Browse only IPPS; the IPP response above must be ignored.
	asm := newAssembler(ServiceIPPS)
	if services := asm.ingest(buildResponse(t)); len(services) != 0 {
		t.Errorf("expected no services for unwanted type, got %+v", services)
	}
}

func TestAssemblerIncompleteWithoutSRV(t *testing.T) {
	name := func(s string) dnsmessage.Name { return dnsmessage.MustNewName(s) }
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{Response: true},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: name("_ipp._tcp.local."), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET},
			Body:   &dnsmessage.PTRResource{PTR: name("Lonely._ipp._tcp.local.")},
		}},
	}
	packed, _ := msg.Pack()
	asm := newAssembler(ServiceIPP)
	// A PTR with no SRV yet is not "ready" (no port to print to).
	if services := asm.ingest(packed); len(services) != 0 {
		t.Errorf("PTR-only service should not be ready, got %+v", services)
	}
}

func TestRecorderWritesToDevicesSeen(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := Service{
		Instance: "Office HP",
		Type:     ServiceIPP,
		Host:     "HP1234.local.",
		Port:     631,
		TXT:      map[string]string{"rp": "ipp/print", "ty": "HP LaserJet 400"},
	}
	rec := Recorder{Store: st}
	ctx, cancel := context.WithCancel(context.Background())

	fixed := time.UnixMilli(1_700_000_000_000)
	done := make(chan struct{})
	go func() {
		_ = rec.Run(ctx, SliceBrowser{Services: []Service{svc}}, func() time.Time { return fixed })
		close(done)
	}()

	// Poll devices_seen until the recorder has written the entry.
	deadline := time.Now().Add(time.Second)
	var found bool
	for time.Now().Before(deadline) {
		devs, _ := st.ListDevices(time.Time{})
		if len(devs) == 1 && devs[0].Name == "HP LaserJet 400" && devs[0].URI == "ipp://HP1234.local:631/ipp/print" {
			found = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if !found {
		t.Fatal("recorder did not write the discovered device into devices_seen")
	}

	// Crucially, discovery must NOT have created a printer.
	printers, _ := st.ListPrinters()
	if len(printers) != 0 {
		t.Errorf("discovery created %d printers; it must create none", len(printers))
	}
}

func TestRepeatedResponsesAreNotReemitted(t *testing.T) {
	asm := newAssembler(ServiceIPP, ServiceIPPS)
	now := time.Unix(1790000000, 0)
	first := asm.changed(asm.ingest(buildResponse(t)), now)
	if len(first) != 1 {
		t.Fatalf("first ingest emitted %d services", len(first))
	}
	for i := 0; i < 1000; i++ {
		if again := asm.changed(asm.ingest(buildResponse(t)), now.Add(time.Second)); len(again) != 0 {
			t.Fatalf("repeat %d re-emitted %d services", i, len(again))
		}
	}
	for host, addrs := range asm.addrsByHost {
		if len(addrs) != 1 {
			t.Fatalf("%s kept %d addresses", host, len(addrs))
		}
	}
	if later := asm.changed(asm.ingest(buildResponse(t)), now.Add(2*time.Minute)); len(later) != 1 {
		t.Fatalf("refresh emitted %d services", len(later))
	}
}

func TestAssemblerSurvivesMalformedRecord(t *testing.T) {
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{Response: true},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("host.local."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
			Body:   &dnsmessage.AResource{A: [4]byte{192, 168, 1, 9}},
		}},
	}
	data, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-5] = 3
	data = data[:len(data)-1]

	done := make(chan struct{})
	go func() {
		newAssembler(ServiceIPP).ingest(data)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ingest did not return on a malformed record")
	}
}
