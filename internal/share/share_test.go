package share

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
)

func TestSelfSignedCert(t *testing.T) {
	cert, err := SelfSignedCert("printer.local")
	if err != nil {
		t.Fatal(err)
	}
	if cert.Leaf == nil || cert.Leaf.Subject.CommonName != "torchio-share" {
		t.Errorf("unexpected cert: %+v", cert.Leaf)
	}
}

func TestShareServerOverIPPS(t *testing.T) {
	// Capture what the share server forwards to the spooler.
	var (
		mu     sync.Mutex
		gotDoc []byte
		gotFmt string
	)
	submit := func(_ context.Context, format string, doc io.Reader) error {
		mu.Lock()
		defer mu.Unlock()
		gotFmt = format
		gotDoc, _ = io.ReadAll(doc)
		return nil
	}

	caps := model.Capabilities{
		MakeAndModel:    "Shared Office Printer",
		State:           3,
		DocumentFormats: []model.MIME{model.MIMEPDF},
		Media:           []string{"iso_a4_210x297mm"},
		Sides:           []string{"one-sided", "two-sided-long-edge"},
	}
	s := New("office", caps, model.MIMEPDF, submit)

	cert, err := SelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ListenTLS("127.0.0.1:0", cert); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx) //nolint:errcheck
	t.Cleanup(func() { s.Close() })
	time.Sleep(20 * time.Millisecond)

	if got := s.URI(); got[:8] != "ipps://1" {
		t.Errorf("share URI = %q, want ipps://...", got)
	}

	// The IPP client (which trusts self-signed printer certs via TOFU) probes
	// and prints over IPPS.
	client := ipp.NewClient()
	msg, err := client.GetPrinterAttributes(ctx, s.URI())
	if err != nil {
		t.Fatalf("GetPrinterAttributes over IPPS: %v", err)
	}
	if pc := ipp.ParseCapabilities(msg); pc.MakeAndModel != "Shared Office Printer" {
		t.Errorf("make/model = %q", pc.MakeAndModel)
	}

	doc := []byte("%PDF-1.7\nshared print\n%%EOF\n")
	if _, err := client.PrintJob(ctx, ipp.PrintRequest{
		PrinterURI:     s.URI(),
		DocumentFormat: "application/pdf",
		Document:       bytes.NewReader(doc),
	}); err != nil {
		t.Fatalf("PrintJob over IPPS: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotFmt != "application/pdf" {
		t.Errorf("forwarded format = %q", gotFmt)
	}
	if !bytes.Equal(gotDoc, doc) {
		t.Errorf("forwarded doc = %q, want %q", gotDoc, doc)
	}
}
