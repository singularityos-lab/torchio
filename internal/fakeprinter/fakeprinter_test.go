package fakeprinter

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
)

func TestFakePrinterProbeAndPrint(t *testing.T) {
	dir := t.TempDir()
	fp := New(Config{
		Name:        "Test Laser",
		Formats:     []model.MIME{model.MIMEPDF},
		Media:       []string{"iso_a4_210x297mm"},
		Sides:       []string{"one-sided", "two-sided-long-edge"},
		Resolutions: []string{"600dpi"},
		Colors:      []string{"monochrome"},
		SaveDir:     dir,
	})
	srv := httptest.NewServer(fp)
	defer srv.Close()

	client := ipp.NewClient()

	// Probe: capabilities reflect the config.
	msg, err := client.GetPrinterAttributes(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	caps := ipp.ParseCapabilities(msg)
	if caps.MakeAndModel != "Test Laser" || !caps.AcceptsFormat(model.MIMEPDF) {
		t.Errorf("caps = %+v", caps)
	}

	// Print a PDF; the fake printer must detect it as PDF and save it.
	pdf := []byte("%PDF-1.7\nhello\n%%EOF\n")
	if _, err := client.PrintJob(context.Background(), ipp.PrintRequest{
		PrinterURI:     srv.URL,
		DocumentFormat: "application/pdf",
		Copies:         2,
		Sides:          "two-sided-long-edge",
		Document:       bytes.NewReader(pdf),
	}); err != nil {
		t.Fatalf("print: %v", err)
	}

	jobs := fp.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	j := jobs[0]
	if j.DetectedFormat != model.MIMEPDF {
		t.Errorf("detected = %s, want PDF", j.DetectedFormat)
	}
	if j.DeclaredFormat != "application/pdf" {
		t.Errorf("declared = %s", j.DeclaredFormat)
	}
	if j.Ticket["copies"] != 2 || j.Ticket["sides"] != "two-sided-long-edge" {
		t.Errorf("ticket = %v", j.Ticket)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "job-001.pdf"))
	if err != nil {
		t.Fatalf("read saved doc: %v", err)
	}
	if !bytes.Equal(saved, pdf) {
		t.Errorf("saved doc mismatch")
	}
}
