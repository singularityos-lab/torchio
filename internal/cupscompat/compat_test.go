package cupscompat

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/model"
)

// fakeBackend records submissions and serves a fixed printer set.
type fakeBackend struct {
	printers []model.Printer
	def      string

	mu        sync.Mutex
	submitted []submission
	nextID    int
}

type submission struct {
	printer string
	name    string
	doc     []byte
}

func (f *fakeBackend) ListPrinters() []model.Printer { return f.printers }
func (f *fakeBackend) DefaultPrinter() (model.Printer, bool) {
	for _, p := range f.printers {
		if p.Name == f.def {
			return p, true
		}
	}
	return model.Printer{}, false
}
func (f *fakeBackend) PrinterByName(name string) (model.Printer, bool) {
	for _, p := range f.printers {
		if p.Name == name {
			return p, true
		}
	}
	return model.Printer{}, false
}
func (f *fakeBackend) Submit(_ context.Context, printer, name string, _ model.JobTicket, doc io.Reader) (string, error) {
	data, _ := io.ReadAll(doc)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.submitted = append(f.submitted, submission{printer: printer, name: name, doc: data})
	return "spool-job", nil
}
func (f *fakeBackend) JobState(string) (model.JobState, bool) { return model.JobSending, true }
func (f *fakeBackend) Cancel(context.Context, string) error   { return nil }

func (f *fakeBackend) lastSubmission() (submission, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.submitted) == 0 {
		return submission{}, false
	}
	return f.submitted[len(f.submitted)-1], true
}

func sampleBackend() *fakeBackend {
	return &fakeBackend{
		def: "office",
		printers: []model.Printer{
			{Name: "office", URI: "ipps://office.local/ipp/print", Caps: model.Capabilities{
				MakeAndModel: "Office Laser", DocumentFormats: []model.MIME{model.MIMEPDF},
				Media: []string{"iso_a4_210x297mm"}, Sides: []string{"one-sided", "two-sided-long-edge"},
				ColorModes: []string{"color", "monochrome"},
			}},
			{Name: "home", URI: "ipp://home.local/ipp/print", Caps: model.Capabilities{
				MakeAndModel: "Home Inkjet", DocumentFormats: []model.MIME{model.MIMEPWGRaster},
			}},
		},
	}
}

// do posts an IPP request (optionally followed by a document) and decodes the
// response, as libcups would.
func do(t *testing.T, url string, msg *goipp.Message, doc []byte) goipp.Message {
	t.Helper()
	payload, err := msg.EncodeBytes()
	if err != nil {
		t.Fatal(err)
	}
	var body io.Reader = bytes.NewReader(payload)
	if doc != nil {
		body = io.MultiReader(bytes.NewReader(payload), bytes.NewReader(doc))
	}
	resp, err := http.Post(url, goipp.ContentType, body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out goipp.Message
	if err := out.Decode(resp.Body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func req(op goipp.Op, id uint32) *goipp.Message {
	m := goipp.NewRequest(goipp.DefaultVersion, op, id)
	m.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	m.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
	return m
}

func serve(t *testing.T, b Backend) string {
	t.Helper()
	srv := httptest.NewServer(New(b))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCupsGetPrinters(t *testing.T) {
	url := serve(t, sampleBackend())
	resp := do(t, url, req(goipp.OpCupsGetPrinters, 1), nil)

	var names []string
	for _, g := range resp.Groups {
		if g.Tag != goipp.TagPrinterGroup {
			continue
		}
		if n, ok := strAttr(g.Attrs, "printer-name"); ok {
			names = append(names, n)
		}
	}
	if len(names) != 2 {
		t.Fatalf("printers = %v, want 2", names)
	}
	// Each printer must carry a CUPS queue URI and make/model.
	foundOffice := false
	for _, g := range resp.Groups {
		if g.Tag != goipp.TagPrinterGroup {
			continue
		}
		if n, _ := strAttr(g.Attrs, "printer-name"); n == "office" {
			foundOffice = true
			if uri, _ := strAttr(g.Attrs, "printer-uri-supported"); uri != "ipp://localhost/printers/office" {
				t.Errorf("queue uri = %q", uri)
			}
			if mm, _ := strAttr(g.Attrs, "printer-make-and-model"); mm != "Office Laser" {
				t.Errorf("make/model = %q", mm)
			}
		}
	}
	if !foundOffice {
		t.Error("office printer not found in CUPS-Get-Printers")
	}
}

func TestCupsGetDefault(t *testing.T) {
	url := serve(t, sampleBackend())
	resp := do(t, url, req(goipp.OpCupsGetDefault, 1), nil)
	if n, _ := strAttr(resp.Printer, "printer-name"); n != "office" {
		t.Errorf("default printer = %q, want office", n)
	}
}

func TestPrintJobRoutesToSpooler(t *testing.T) {
	b := sampleBackend()
	url := serve(t, b)

	m := req(goipp.OpPrintJob, 2)
	m.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String("ipp://localhost/printers/office")))
	m.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String("report")))
	m.Job.Add(goipp.MakeAttribute("copies", goipp.TagInteger, goipp.Integer(2)))

	pdf := []byte("%PDF-1.7\nlibcups job\n%%EOF\n")
	resp := do(t, url, m, pdf)

	if _, ok := intAttr(resp.Job, "job-id"); !ok {
		t.Fatal("response missing job-id")
	}
	sub, ok := b.lastSubmission()
	if !ok {
		t.Fatal("nothing submitted to the spooler")
	}
	if sub.printer != "office" {
		t.Errorf("submitted to %q, want office", sub.printer)
	}
	if !bytes.Equal(sub.doc, pdf) {
		t.Errorf("submitted doc mismatch: %q", sub.doc)
	}
}

// TestCreateJobSendDocument exercises the GTK/libcups flow.
func TestCreateJobSendDocument(t *testing.T) {
	b := sampleBackend()
	url := serve(t, b)

	// Create-Job
	c := req(goipp.OpCreateJob, 3)
	c.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String("ipp://localhost/printers/home")))
	c.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String("gtk job")))
	respC := do(t, url, c, nil)
	jobID, ok := intAttr(respC.Job, "job-id")
	if !ok {
		t.Fatal("Create-Job returned no job-id")
	}

	// Nothing submitted yet (no document).
	if _, ok := b.lastSubmission(); ok {
		t.Fatal("spooler got a job before any document was sent")
	}

	// Send-Document (last)
	sd := req(goipp.OpSendDocument, 4)
	sd.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String("ipp://localhost/printers/home")))
	sd.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(jobID)))
	sd.Operation.Add(goipp.MakeAttribute("last-document", goipp.TagBoolean, goipp.Boolean(true)))
	doc := []byte("%PDF-1.7\ndeferred document\n%%EOF\n")
	do(t, url, sd, doc)

	sub, ok := b.lastSubmission()
	if !ok {
		t.Fatal("document not submitted after last Send-Document")
	}
	if sub.printer != "home" || !bytes.Equal(sub.doc, doc) {
		t.Errorf("submission = %q / %q", sub.printer, sub.doc)
	}

	// Get-Job-Attributes reports a live state.
	ga := req(goipp.OpGetJobAttributes, 5)
	ga.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(jobID)))
	respG := do(t, url, ga, nil)
	if st, ok := intAttr(respG.Job, "job-state"); !ok || st != ippJobProcessing {
		t.Errorf("job-state = %d/%v, want processing(5)", st, ok)
	}
}
