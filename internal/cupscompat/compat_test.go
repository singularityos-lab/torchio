package cupscompat

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/model"
)

// fakeBackend records submissions and serves a fixed printer set.
type fakeBackend struct {
	printers []model.Printer
	def      string

	mu        sync.Mutex
	submitted []submission
	jobs      []Job
	actions   []string
}

type submission struct {
	printer string
	name    string
	doc     []byte
}

func (f *fakeBackend) ListPrinters() []model.Printer { return f.printers }
func (f *fakeBackend) DefaultPrinter() (model.Printer, bool) {
	return f.PrinterByName(f.def)
}
func (f *fakeBackend) PrinterByName(name string) (model.Printer, bool) {
	for _, p := range f.printers {
		if p.Name == name {
			return p, true
		}
	}
	return model.Printer{}, false
}
func (f *fakeBackend) Submit(_ context.Context, printer, name string, _ model.JobTicket, doc io.Reader) (Job, error) {
	data, _ := io.ReadAll(doc)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, submission{printer: printer, name: name, doc: data})
	j := Job{Number: len(f.jobs) + 1, Printer: printer, Name: name, State: model.JobSending, Created: time.Now(), Updated: time.Now()}
	f.jobs = append(f.jobs, j)
	return j, nil
}
func (f *fakeBackend) Create(_ context.Context, printer, name string, _ model.JobTicket) (Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := Job{Number: len(f.jobs) + 1, Printer: printer, Name: name, State: model.JobCreated, Created: time.Now(), Updated: time.Now()}
	f.jobs = append(f.jobs, j)
	return j, nil
}
func (f *fakeBackend) Attach(_ context.Context, n int, doc io.Reader) (Job, error) {
	data, _ := io.ReadAll(doc)
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.jobs {
		if f.jobs[i].Number == n {
			f.jobs[i].State = model.JobSending
			f.submitted = append(f.submitted, submission{printer: f.jobs[i].Printer, name: f.jobs[i].Name, doc: data})
			return f.jobs[i], nil
		}
	}
	return Job{}, ErrNotFound
}
func (f *fakeBackend) Job(n int) (Job, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.Number == n {
			return j, true
		}
	}
	return Job{}, false
}
func (f *fakeBackend) Jobs(printer, which string) []Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Job
	for _, j := range f.jobs {
		if printer != "" && j.Printer != printer {
			continue
		}
		done := j.State.IsTerminal()
		if (which == WhichActive && done) || (which == WhichFinished && !done) {
			continue
		}
		out = append(out, j)
	}
	return out
}
func (f *fakeBackend) JobAction(_ context.Context, n int, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.jobs {
		if f.jobs[i].Number != n {
			continue
		}
		f.actions = append(f.actions, action)
		switch action {
		case ActionHold:
			f.jobs[i].State = model.JobHeld
		case ActionRelease:
			f.jobs[i].State = model.JobPending
		case ActionCancel:
			f.jobs[i].State = model.JobCanceled
		}
		return nil
	}
	return ErrNotFound
}
func (f *fakeBackend) AddOrModifyPrinter(_ context.Context, name string, c PrinterChange) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.printers {
		if f.printers[i].Name != name {
			continue
		}
		q := &f.printers[i].Queue
		if c.Info != nil {
			q.Info = *c.Info
		}
		if c.Paused != nil {
			q.Paused = *c.Paused
		}
		if c.Accepting != nil {
			q.Rejecting = !*c.Accepting
		}
		return nil
	}
	if c.URI == nil {
		return ErrNotPossible
	}
	f.printers = append(f.printers, model.Printer{Name: name, URI: *c.URI})
	return nil
}
func (f *fakeBackend) DeletePrinter(name string) error {
	for i, p := range f.printers {
		if p.Name == name {
			f.printers = append(f.printers[:i], f.printers[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}
func (f *fakeBackend) SetDefault(name string) error {
	if _, ok := f.PrinterByName(name); !ok {
		return ErrNotFound
	}
	f.def = name
	return nil
}
func (f *fakeBackend) Devices(context.Context) []Device {
	return []Device{{URI: "ipps://new.local/ipp/print", Info: "New Printer", MakeModel: "New Printer", Class: "network"}}
}

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

func onPrinter(op goipp.Op, id uint32, name string) *goipp.Message {
	m := req(op, id)
	m.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String("ipp://localhost/printers/"+name)))
	return m
}

func printJob(t *testing.T, url, printer string) int {
	t.Helper()
	resp := do(t, url, onPrinter(goipp.OpPrintJob, 2, printer), []byte("%PDF-1.7\n%%EOF\n"))
	id, ok := intAttr(resp.Job, "job-id")
	if !ok {
		t.Fatal("Print-Job returned no job-id")
	}
	return id
}

func TestHoldReleaseAndGetJobs(t *testing.T) {
	b := sampleBackend()
	url := serve(t, b)
	id := printJob(t, url, "office")

	h := req(goipp.OpHoldJob, 3)
	h.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	if r := do(t, url, h, nil); goipp.Status(r.Code) != goipp.StatusOk {
		t.Fatalf("Hold-Job status %s", goipp.Status(r.Code))
	}
	ga := req(goipp.OpGetJobAttributes, 4)
	ga.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	if st, _ := intAttr(do(t, url, ga, nil).Job, "job-state"); st != ippJobHeld {
		t.Errorf("job-state after hold = %d, want %d", st, ippJobHeld)
	}

	r := req(goipp.OpReleaseJob, 5)
	r.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	do(t, url, r, nil)

	c := req(goipp.OpCancelJob, 6)
	c.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	do(t, url, c, nil)

	gj := onPrinter(goipp.OpGetJobs, 7, "office")
	gj.Operation.Add(goipp.MakeAttribute("which-jobs", goipp.TagKeyword, goipp.String("completed")))
	jobs := 0
	for _, g := range do(t, url, gj, nil).Groups {
		if g.Tag == goipp.TagJobGroup {
			jobs++
		}
	}
	if jobs != 1 {
		t.Errorf("completed jobs = %d, want 1", jobs)
	}
	if got := len(b.actions); got != 3 {
		t.Errorf("actions = %v", b.actions)
	}
}

func TestPauseShowsStoppedPrinter(t *testing.T) {
	b := sampleBackend()
	url := serve(t, b)
	if r := do(t, url, onPrinter(goipp.OpPausePrinter, 2, "office"), nil); goipp.Status(r.Code) != goipp.StatusOk {
		t.Fatalf("Pause-Printer status %s", goipp.Status(r.Code))
	}
	attrs := do(t, url, onPrinter(goipp.OpGetPrinterAttributes, 3, "office"), nil).Printer
	if st, _ := intAttr(attrs, "printer-state"); st != 5 {
		t.Errorf("printer-state = %d, want 5", st)
	}
	if reason, _ := strAttr(attrs, "printer-state-reasons"); reason != "paused" {
		t.Errorf("printer-state-reasons = %q", reason)
	}
}

func TestAddModifyDeleteAndDefault(t *testing.T) {
	b := sampleBackend()
	url := serve(t, b)

	m := onPrinter(goipp.OpCupsAddModifyPrinter, 2, "office")
	m.Printer.Add(goipp.MakeAttribute("printer-info", goipp.TagText, goipp.String("Second floor")))
	do(t, url, m, nil)
	attrs := do(t, url, onPrinter(goipp.OpGetPrinterAttributes, 3, "office"), nil).Printer
	if info, _ := strAttr(attrs, "printer-info"); info != "Second floor" {
		t.Errorf("printer-info = %q", info)
	}

	add := onPrinter(goipp.OpCupsAddModifyPrinter, 4, "new")
	add.Printer.Add(goipp.MakeAttribute("device-uri", goipp.TagURI, goipp.String("ipps://new.local/ipp/print")))
	do(t, url, add, nil)
	if _, ok := b.PrinterByName("new"); !ok {
		t.Fatal("CUPS-Add-Modify-Printer did not add the queue")
	}

	do(t, url, onPrinter(goipp.OpCupsSetDefault, 5, "new"), nil)
	if n, _ := strAttr(do(t, url, req(goipp.OpCupsGetDefault, 6), nil).Printer, "printer-name"); n != "new" {
		t.Errorf("default = %q, want new", n)
	}

	do(t, url, onPrinter(goipp.OpCupsDeletePrinter, 7, "new"), nil)
	if _, ok := b.PrinterByName("new"); ok {
		t.Error("CUPS-Delete-Printer left the queue")
	}
	if r := do(t, url, onPrinter(goipp.OpCupsDeletePrinter, 8, "missing"), nil); goipp.Status(r.Code) != goipp.StatusErrorNotFound {
		t.Errorf("delete missing = %s", goipp.Status(r.Code))
	}
}

func TestGetDevices(t *testing.T) {
	url := serve(t, sampleBackend())
	var uris []string
	for _, g := range do(t, url, req(goipp.OpCupsGetDevices, 2), nil).Groups {
		if u, ok := strAttr(g.Attrs, "device-uri"); ok && g.Tag == goipp.TagPrinterGroup {
			uris = append(uris, u)
		}
	}
	if len(uris) != 1 || uris[0] != "ipps://new.local/ipp/print" {
		t.Errorf("devices = %v", uris)
	}
}
