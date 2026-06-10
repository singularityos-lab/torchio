package ipp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenPrinting/goipp"
)

// fakePrinter is an in-process IPP endpoint for tests. It decodes the request,
// records what it saw, and returns a canned response. Because goipp.Decode
// consumes exactly the message bytes, anything left in the body after decoding
// is the print document, which lets us assert the Print-Job wire format.
type fakePrinter struct {
	printerAttrs goipp.Attributes
	forceStatus  goipp.Status // when non-zero, returned instead of StatusOk

	lastOp  goipp.Op
	lastDoc []byte
	lastReq goipp.Message
}

func (f *fakePrinter) handler(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != goipp.ContentType {
		http.Error(w, "bad content type "+ct, http.StatusBadRequest)
		return
	}

	var req goipp.Message
	if err := req.Decode(r.Body); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	f.lastReq = req
	f.lastOp = goipp.Op(req.Code)
	doc, _ := io.ReadAll(r.Body)
	f.lastDoc = doc

	status := goipp.StatusOk
	if f.forceStatus != 0 {
		status = f.forceStatus
	}

	resp := goipp.NewResponse(goipp.DefaultVersion, status, req.RequestID)
	resp.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	resp.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))

	switch f.lastOp {
	case goipp.OpGetPrinterAttributes:
		resp.Printer = f.printerAttrs
	case goipp.OpPrintJob:
		if status == goipp.StatusOk {
			resp.Job.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(42)))
			resp.Job.Add(goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(3)))
			resp.Job.Add(goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String("ipp://printer/jobs/42")))
		}
	}

	payload, err := resp.EncodeBytes()
	if err != nil {
		http.Error(w, "encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", goipp.ContentType)
	_, _ = w.Write(payload)
}

func newFakeServer(t *testing.T, f *fakePrinter) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return srv, srv.URL // http://127.0.0.1:port, accepted by normalizeURI
}

func sampleAttrs() goipp.Attributes {
	var a goipp.Attributes
	a.Add(goipp.MakeAttribute("printer-make-and-model", goipp.TagText, goipp.String("Torchio Test Laser")))
	a.Add(goipp.MakeAttribute("printer-state", goipp.TagEnum, goipp.Integer(3)))
	a.Add(goipp.MakeAttr("document-format-supported", goipp.TagMimeType,
		goipp.String("application/pdf"), goipp.String("image/pwg-raster"), goipp.String("image/urf")))
	a.Add(goipp.MakeAttr("media-supported", goipp.TagKeyword,
		goipp.String("iso_a4_210x297mm"), goipp.String("na_letter_8.5x11in")))
	a.Add(goipp.MakeAttr("sides-supported", goipp.TagKeyword,
		goipp.String("one-sided"), goipp.String("two-sided-long-edge")))
	a.Add(goipp.MakeAttribute("printer-resolution-supported", goipp.TagResolution,
		goipp.Resolution{Xres: 600, Yres: 600, Units: goipp.UnitsDpi}))
	a.Add(goipp.MakeAttr("print-color-mode-supported", goipp.TagKeyword,
		goipp.String("monochrome"), goipp.String("color")))
	return a
}

func TestGetPrinterAttributesAndParse(t *testing.T) {
	f := &fakePrinter{printerAttrs: sampleAttrs()}
	_, url := newFakeServer(t, f)

	client := NewClient()
	msg, err := client.GetPrinterAttributes(context.Background(), url)
	if err != nil {
		t.Fatalf("GetPrinterAttributes: %v", err)
	}
	if f.lastOp != goipp.OpGetPrinterAttributes {
		t.Fatalf("server saw op %v, want Get-Printer-Attributes", f.lastOp)
	}

	caps := ParseCapabilities(msg)
	if caps.MakeAndModel != "Torchio Test Laser" {
		t.Errorf("make/model = %q", caps.MakeAndModel)
	}
	if caps.State != 3 {
		t.Errorf("state = %d, want 3", caps.State)
	}
	if !caps.AcceptsFormat("application/pdf") {
		t.Errorf("expected PDF support, formats = %v", caps.DocumentFormats)
	}
	if caps.AcceptsFormat("application/postscript") {
		t.Errorf("did not expect PostScript support")
	}
	if !caps.SupportsDuplex() {
		t.Errorf("expected duplex support, sides = %v", caps.Sides)
	}
	if len(caps.Resolutions) != 1 || caps.Resolutions[0] != "600dpi" {
		t.Errorf("resolutions = %v, want [600dpi]", caps.Resolutions)
	}
	if len(caps.Media) != 2 {
		t.Errorf("media = %v, want 2 entries", caps.Media)
	}
}

func TestPrintJobWireFormat(t *testing.T) {
	f := &fakePrinter{printerAttrs: sampleAttrs()}
	_, url := newFakeServer(t, f)

	doc := []byte("%PDF-1.7\nfake pdf body\n%%EOF\n")
	client := NewClient(WithUserName("tester"))
	res, err := client.PrintJob(context.Background(), PrintRequest{
		PrinterURI:     url,
		JobName:        "unit test",
		DocumentFormat: "application/pdf",
		Copies:         2,
		Sides:          "two-sided-long-edge",
		Media:          "iso_a4_210x297mm",
		Document:       bytes.NewReader(doc),
	})
	if err != nil {
		t.Fatalf("PrintJob: %v", err)
	}
	if res.JobID != 42 {
		t.Errorf("job-id = %d, want 42", res.JobID)
	}
	if f.lastOp != goipp.OpPrintJob {
		t.Errorf("server saw op %v, want Print-Job", f.lastOp)
	}
	// The document must arrive verbatim, immediately after the IPP header.
	if !bytes.Equal(f.lastDoc, doc) {
		t.Errorf("document mismatch:\n got %q\nwant %q", f.lastDoc, doc)
	}
	// And the job template attributes must be in the Job group.
	if v, ok := FirstInt(f.lastReq.Job, "copies"); !ok || v != 2 {
		t.Errorf("copies attr = %d/%v, want 2", v, ok)
	}
	if v, ok := FirstString(f.lastReq.Job, "sides"); !ok || v != "two-sided-long-edge" {
		t.Errorf("sides attr = %q/%v", v, ok)
	}
	if v, ok := FirstString(f.lastReq.Operation, "requesting-user-name"); !ok || v != "tester" {
		t.Errorf("user attr = %q/%v, want tester", v, ok)
	}
}

func TestValidateJobStatusError(t *testing.T) {
	f := &fakePrinter{printerAttrs: sampleAttrs(), forceStatus: goipp.StatusErrorAttributesOrValues}
	_, url := newFakeServer(t, f)

	client := NewClient()
	err := client.ValidateJob(context.Background(), PrintRequest{
		PrinterURI:     url,
		DocumentFormat: "application/pdf",
		Sides:          "nonsense-mode",
	})
	if err == nil {
		t.Fatal("expected a StatusError, got nil")
	}
	se, ok := AsStatusError(err)
	if !ok {
		t.Fatalf("error is not a StatusError: %v", err)
	}
	if se.Status != goipp.StatusErrorAttributesOrValues {
		t.Errorf("status = %v, want attributes-or-values-not-supported", se.Status)
	}
}

func TestGetJobAttributesAndCancel(t *testing.T) {
	f := &fakePrinter{printerAttrs: sampleAttrs()}
	_, url := newFakeServer(t, f)
	client := NewClient()

	if _, err := client.GetJobAttributes(context.Background(), url, 42); err != nil {
		t.Fatalf("GetJobAttributes: %v", err)
	}
	if f.lastOp != goipp.OpGetJobAttributes {
		t.Errorf("op = %v, want Get-Job-Attributes", f.lastOp)
	}
	if v, ok := FirstInt(f.lastReq.Operation, "job-id"); !ok || v != 42 {
		t.Errorf("job-id attr = %d/%v", v, ok)
	}

	if err := client.CancelJob(context.Background(), url, 42); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	if f.lastOp != goipp.OpCancelJob {
		t.Errorf("op = %v, want Cancel-Job", f.lastOp)
	}
}
