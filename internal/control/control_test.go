package control

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/mirkobrombin/go-foundation/pkg/events"

	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/portal"
	"github.com/singularityos-lab/torchio/internal/queue"
	"github.com/singularityos-lab/torchio/internal/spool"
	"github.com/singularityos-lab/torchio/internal/store"
)

// fakeTransport is a no-network printer for the spooler side.
type fakeTransport struct{ doc []byte }

func (f *fakeTransport) PrintJob(ctx context.Context, r ipp.PrintRequest) (*ipp.PrintResult, error) {
	f.doc, _ = io.ReadAll(r.Document)
	return &ipp.PrintResult{JobID: 11}, nil
}

// fakeIPPServer answers Get-Printer-Attributes so AddPrinter's probe succeeds
// without a real printer.
func fakeIPPServer(t *testing.T) string {
	t.Helper()
	h := func(w http.ResponseWriter, r *http.Request) {
		var req goipp.Message
		_ = req.Decode(r.Body)
		resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, req.RequestID)
		resp.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
		resp.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
		resp.Printer.Add(goipp.MakeAttribute("printer-make-and-model", goipp.TagText, goipp.String("Fake IPP")))
		resp.Printer.Add(goipp.MakeAttribute("document-format-supported", goipp.TagMimeType, goipp.String("application/pdf")))
		payload, _ := resp.EncodeBytes()
		w.Header().Set("Content-Type", goipp.ContentType)
		_, _ = w.Write(payload)
	}
	srv := httptest.NewServer(http.HandlerFunc(h))
	t.Cleanup(srv.Close)
	return srv.URL
}

// startDaemon wires the full control stack on a temp socket and returns a
// connected client. The portal gates trust decisions (discovered-printer adds).
func startDaemon(t *testing.T, tr queue.Transport, trust portal.Portal) (*Client, *store.Store) {
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
	eng := queue.New(queue.Config{Store: st, Spool: sp, Bus: events.New(), Transport: tr})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)

	svc := NewService(st, eng, trust)
	srv, err := NewServer(svc)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "control.sock")
	if err := srv.Listen(socket); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ctx) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	// Give Serve a moment to be accepting.
	time.Sleep(20 * time.Millisecond)

	client, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client, st
}

func TestControlEndToEnd(t *testing.T) {
	tr := &fakeTransport{}
	client, _ := startDaemon(t, tr, portal.AutoApprove{})
	uri := fakeIPPServer(t)

	// Add a printer through the RPC (exercises the probe path).
	p, err := client.AddPrinter(AddPrinterArgs{Name: "office", URI: uri, MakeDefault: true})
	if err != nil {
		t.Fatalf("AddPrinter: %v", err)
	}
	if p.MakeAndModel != "Fake IPP" || len(p.Formats) != 1 {
		t.Errorf("probed printer = %+v", p)
	}

	// It should appear in the list and be the default.
	printers, err := client.ListPrinters()
	if err != nil {
		t.Fatal(err)
	}
	if len(printers) != 1 || !printers[0].Default {
		t.Fatalf("printers = %+v", printers)
	}

	// Submit a PDF to the default printer.
	pdf := []byte("%PDF-1.7\nhello via control plane\n%%EOF\n")
	jobID, err := client.Submit(SubmitArgs{Name: "doc.pdf", Doc: pdf, Ticket: TicketDTO{Copies: 1}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Poll until the job completes.
	deadline := time.Now().Add(2 * time.Second)
	var done JobDTO
	for time.Now().Before(deadline) {
		jobs, err := client.ListJobs(ListJobsArgs{})
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 1 && jobs[0].State == "completed" {
			done = jobs[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if done.ID != jobID {
		t.Fatalf("job %s did not complete (got %+v)", jobID, done)
	}

	// The fake printer must have received the document.
	if string(tr.doc) != string(pdf) {
		t.Errorf("printer received %q, want %q", tr.doc, pdf)
	}

	// History must show the full transition chain.
	h, err := client.JobHistory(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Events) == 0 {
		t.Error("expected a non-empty job history")
	}
	if h.Job.State != "completed" {
		t.Errorf("history job state = %s", h.Job.State)
	}
}

func TestControlRejectsUnknownPrinter(t *testing.T) {
	client, _ := startDaemon(t, &fakeTransport{}, portal.AutoApprove{})
	_, err := client.Submit(SubmitArgs{Name: "x", Doc: []byte("%PDF-1.7\n")})
	if err == nil {
		t.Fatal("expected error submitting with no default printer")
	}
}

// TestAddDiscoveredApproved confirms that a discovered device becomes a real
// printer (tagged discovered+confirmed) only after portal approval.
func TestAddDiscoveredApproved(t *testing.T) {
	client, st := startDaemon(t, &fakeTransport{}, portal.AutoApprove{})
	uri := fakeIPPServer(t)

	// Simulate what the discovery recorder would have written.
	if err := st.RecordDevice(store.SeenDevice{Key: "HP._ipp._tcp", Name: "HP LaserJet", URI: uri, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}

	devices, err := client.ListDiscovered()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Key != "HP._ipp._tcp" {
		t.Fatalf("discovered = %+v", devices)
	}

	p, err := client.AddDiscovered(AddDiscoveredArgs{Key: "HP._ipp._tcp", Name: "front-desk"})
	if err != nil {
		t.Fatalf("AddDiscovered: %v", err)
	}
	if p.Origin != "discovered+confirmed" {
		t.Errorf("origin = %q, want discovered+confirmed", p.Origin)
	}
	printers, _ := client.ListPrinters()
	if len(printers) != 1 || printers[0].Name != "front-desk" {
		t.Errorf("printers = %+v", printers)
	}
}

// TestAddDiscoveredDeniedCreatesNothing is the security-critical case: if the
// user denies through the portal, no printer is created.
func TestAddDiscoveredDeniedCreatesNothing(t *testing.T) {
	client, st := startDaemon(t, &fakeTransport{}, portal.AlwaysDeny{})
	uri := fakeIPPServer(t)
	if err := st.RecordDevice(store.SeenDevice{Key: "HP._ipp._tcp", Name: "HP LaserJet", URI: uri, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}

	_, err := client.AddDiscovered(AddDiscoveredArgs{Key: "HP._ipp._tcp"})
	if err == nil {
		t.Fatal("expected AddDiscovered to fail when the portal denies")
	}
	printers, _ := client.ListPrinters()
	if len(printers) != 0 {
		t.Errorf("a denied add must create no printer, got %d", len(printers))
	}
}
