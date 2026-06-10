package queue

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mirkobrombin/go-foundation/pkg/events"

	"github.com/singularityos-lab/torchio/internal/detect"
	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/spool"
	"github.com/singularityos-lab/torchio/internal/store"
	"github.com/singularityos-lab/torchio/internal/transform"
)

// fakeTransport records what it was asked to print and returns a scripted
// result or error. It stands in for a real IPP printer.
type fakeTransport struct {
	mu     sync.Mutex
	gotDoc []byte
	gotReq ipp.PrintRequest
	calls  int
	err    error // returned from PrintJob when set
	failCt int   // fail this many initial calls, then succeed (transient)
}

func (f *fakeTransport) PrintJob(ctx context.Context, r ipp.PrintRequest) (*ipp.PrintResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	doc, _ := io.ReadAll(r.Document)
	f.gotDoc = doc
	f.gotReq = r
	if f.failCt > 0 {
		f.failCt--
		return nil, errTransient
	}
	if f.err != nil {
		return nil, f.err
	}
	return &ipp.PrintResult{JobID: 7, JobState: 3}, nil
}

type tempError struct{}

func (tempError) Error() string { return "temporary network blip" }

var errTransient = tempError{}

// harness wires a real store, spool and bus with a fake transport.
func harness(t *testing.T, tr Transport) (*Engine, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sp, err := spool.Open(filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	eng := New(Config{Store: st, Spool: sp, Bus: events.New(), Transport: tr})
	return eng, st
}

func pdfCapablePrinter(t *testing.T, st *store.Store) *model.Printer {
	t.Helper()
	now := time.Now()
	p := &model.Printer{
		ID:      model.NewID(),
		Name:    "test",
		URI:     "ipps://test.local:631/ipp/print",
		Origin:  model.OriginManual,
		Created: now,
		Updated: now,
		Caps:    model.Capabilities{DocumentFormats: []model.MIME{model.MIMEPDF}},
	}
	if err := st.SavePrinter(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// waitState polls until the job reaches want or the timeout fires.
func waitState(t *testing.T, st *store.Store, id model.ID, want model.JobState, timeout time.Duration) *model.Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		j, err := st.GetJob(id)
		if err == nil && model.JobState(j.State) == want {
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	j, _ := st.GetJob(id)
	state := "missing"
	if j != nil {
		state = j.State
	}
	t.Fatalf("job %s did not reach %s within %s (last state %s)", id, want, timeout, state)
	return nil
}

func TestEngineHappyPath(t *testing.T) {
	tr := &fakeTransport{}
	eng, st := harness(t, tr)
	p := pdfCapablePrinter(t, st)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Stop()

	pdf := []byte("%PDF-1.7\nthe whole document\n%%EOF\n")
	job, err := eng.Submit(ctx, p.ID, "doc.pdf", model.JobTicket{Copies: 1}, bytes.NewReader(pdf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	done := waitState(t, st, job.ID, model.JobCompleted, 2*time.Second)
	if done.RemoteJobID != 7 {
		t.Errorf("remote job id = %d, want 7", done.RemoteJobID)
	}

	// The printer must have received the exact document bytes and format.
	tr.mu.Lock()
	gotDoc, gotFmt := tr.gotDoc, tr.gotReq.DocumentFormat
	tr.mu.Unlock()
	if !bytes.Equal(gotDoc, pdf) {
		t.Errorf("transport got %q, want %q", gotDoc, pdf)
	}
	if gotFmt != "application/pdf" {
		t.Errorf("document-format = %q", gotFmt)
	}

	// The event log must record the full path.
	events, err := st.JobEvents(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var transitions []model.JobState
	for _, e := range events {
		if e.Kind == model.EventTransition {
			transitions = append(transitions, e.To)
		}
	}
	want := []model.JobState{
		model.JobValidating, model.JobPending, model.JobSpooled,
		model.JobSending, model.JobCompleted,
	}
	if len(transitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", transitions, want)
	}
	for i := range want {
		if transitions[i] != want[i] {
			t.Errorf("transition %d = %s, want %s", i, transitions[i], want[i])
		}
	}
}

func TestEngineTransformsImageToPDF(t *testing.T) {
	tr := &fakeTransport{}
	eng, st := harness(t, tr)
	// Printer accepts only PDF; we submit a PNG, so the engine must transform.
	p := pdfCapablePrinter(t, st)

	ctx := context.Background()
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Stop()

	// A small PNG image.
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 30), B: 200, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}

	job, err := eng.Submit(ctx, p.ID, "photo.png", model.JobTicket{}, &pngBuf)
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, st, job.ID, model.JobCompleted, 3*time.Second)

	// The job's document must now be a PDF (the transformed blob).
	if done.Document.Format != model.MIMEPDF {
		t.Errorf("job document format = %s, want application/pdf", done.Document.Format)
	}

	// The printer must have received a PDF, not the original PNG.
	tr.mu.Lock()
	gotDoc, gotFmt := tr.gotDoc, tr.gotReq.DocumentFormat
	tr.mu.Unlock()
	if gotFmt != "application/pdf" {
		t.Errorf("sent document-format = %q, want application/pdf", gotFmt)
	}
	if detect.FormatBytes(gotDoc) != model.MIMEPDF {
		t.Errorf("printer did not receive a PDF (got %v)", detect.FormatBytes(gotDoc))
	}

	// The history must include the transforming state.
	events, _ := st.JobEvents(job.ID)
	var sawTransforming bool
	for _, e := range events {
		if e.To == model.JobTransforming {
			sawTransforming = true
		}
	}
	if !sawTransforming {
		t.Error("expected the job to pass through the transforming state")
	}
}

// TestEngineRasterizesPDFForRasterPrinter is the full raster path with pdfium: a
// PDF is submitted to a printer that accepts only PWG raster, so the engine
// must run the pdfium PDF->raster transform and send raster bytes.
func TestEngineRasterizesPDFForRasterPrinter(t *testing.T) {
	tr := &fakeTransport{}

	// Engine with native transforms plus pdfium.
	dir := t.TempDir()
	st, err := store.Open(dir + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sp, err := spool.Open(dir + "/spool")
	if err != nil {
		t.Fatal(err)
	}
	reg := transform.DefaultRegistry()
	pdfium := transform.NewPDFiumTransform(150)
	t.Cleanup(func() { pdfium.Close() })
	reg.Register(pdfium)
	eng := New(Config{Store: st, Spool: sp, Bus: events.New(), Transport: tr, Transforms: reg})

	// Printer accepts only PWG raster.
	now := time.Now()
	p := &model.Printer{
		ID: model.NewID(), Name: "raster", URI: "ipps://r.local/ipp/print",
		Origin: model.OriginManual, Created: now, Updated: now,
		Caps: model.Capabilities{DocumentFormats: []model.MIME{model.MIMEPWGRaster}},
	}
	if err := st.SavePrinter(p); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Stop()

	// Build a real PDF using the native image->PDF transform.
	img := image.NewRGBA(image.Rect(0, 0, 24, 18))
	for y := 0; y < 18; y++ {
		for x := 0; x < 24; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 8), G: 40, B: uint8(y * 10), A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	toPDF, ok := transform.DefaultRegistry().Plan(model.MIMEPNG, []model.MIME{model.MIMEPDF})
	if !ok || len(toPDF) != 1 {
		t.Fatal("could not plan image->PDF")
	}
	var pdfBuf bytes.Buffer
	if err := toPDF[0].Run(ctx, &pngBuf, &pdfBuf, transform.Params{}); err != nil {
		t.Fatalf("build PDF: %v", err)
	}

	job, err := eng.Submit(ctx, p.ID, "doc.pdf", model.JobTicket{}, &pdfBuf)
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, st, job.ID, model.JobCompleted, 30*time.Second)

	if done.Document.Format != model.MIMEPWGRaster {
		t.Errorf("job document format = %s, want image/pwg-raster", done.Document.Format)
	}
	tr.mu.Lock()
	gotDoc, gotFmt := tr.gotDoc, tr.gotReq.DocumentFormat
	tr.mu.Unlock()
	if gotFmt != "image/pwg-raster" {
		t.Errorf("sent document-format = %q, want image/pwg-raster", gotFmt)
	}
	if detect.FormatBytes(gotDoc) != model.MIMEPWGRaster {
		t.Errorf("printer did not receive PWG raster (got %v)", detect.FormatBytes(gotDoc))
	}
}

func TestEngineAbortsUnsupportedFormat(t *testing.T) {
	tr := &fakeTransport{}
	eng, st := harness(t, tr)
	// Printer only accepts PWG raster, not the plain text we will submit.
	now := time.Now()
	p := &model.Printer{
		ID: model.NewID(), Name: "raster-only", URI: "ipps://r.local/ipp/print",
		Origin: model.OriginManual, Created: now, Updated: now,
		Caps: model.Capabilities{DocumentFormats: []model.MIME{model.MIMEPWGRaster}},
	}
	if err := st.SavePrinter(p); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Stop()

	job, err := eng.Submit(ctx, p.ID, "notes.txt", model.JobTicket{}, bytes.NewReader([]byte("just some plain text, no transform available")))
	if err != nil {
		t.Fatal(err)
	}
	aborted := waitState(t, st, job.ID, model.JobAborted, 2*time.Second)
	if aborted.Error == "" {
		t.Error("aborted job should carry an error cause")
	}
	tr.mu.Lock()
	calls := tr.calls
	tr.mu.Unlock()
	if calls != 0 {
		t.Errorf("transport should not be called for an unsupported format, got %d calls", calls)
	}
}

func TestEngineRetriesTransientThenSucceeds(t *testing.T) {
	tr := &fakeTransport{failCt: 2} // first two sends fail transiently
	eng, st := harness(t, tr)
	p := pdfCapablePrinter(t, st)

	ctx := context.Background()
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Stop()

	job, err := eng.Submit(ctx, p.ID, "doc.pdf", model.JobTicket{}, bytes.NewReader([]byte("%PDF-1.7\nretry me\n")))
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, st, job.ID, model.JobCompleted, 3*time.Second)
	tr.mu.Lock()
	calls := tr.calls
	tr.mu.Unlock()
	if calls != 3 {
		t.Errorf("expected 3 PrintJob attempts (2 fail + 1 ok), got %d", calls)
	}
}

func TestEnginePermanentStatusErrorAborts(t *testing.T) {
	tr := &fakeTransport{err: &ipp.StatusError{Status: 0x0400, Detail: "bad request"}}
	eng, st := harness(t, tr)
	p := pdfCapablePrinter(t, st)

	ctx := context.Background()
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Stop()

	job, err := eng.Submit(ctx, p.ID, "doc.pdf", model.JobTicket{}, bytes.NewReader([]byte("%PDF-1.7\nreject me\n")))
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, st, job.ID, model.JobAborted, 2*time.Second)
	// A permanent IPP status error must not be retried.
	tr.mu.Lock()
	calls := tr.calls
	tr.mu.Unlock()
	if calls != 1 {
		t.Errorf("permanent error should not retry, got %d calls", calls)
	}
}
