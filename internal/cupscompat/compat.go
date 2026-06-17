// Package cupscompat makes Torchio a drop-in for CUPS: it answers the IPP
// dialect that libcups speaks (the CUPS-Get-Printers / CUPS-Get-Default
// extensions plus Create-Job/Send-Document) on the CUPS socket, so existing
// apps (GTK and Qt print dialogs, lp/lpstat, Chromium, anything using libcups)
// enumerate and print to Torchio's queues without knowing anything changed.
// The printers it advertises are Torchio's; the jobs it accepts go to the
// spooler. cupsd must not be running (Torchio owns its socket).
package cupscompat

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/model"
)

// Backend is the slice of Torchio the compat server needs: the configured
// printers and a way to submit/track jobs. The daemon implements it over the
// store and queue engine; tests use a fake.
type Backend interface {
	ListPrinters() []model.Printer
	DefaultPrinter() (model.Printer, bool)
	PrinterByName(name string) (model.Printer, bool)
	Submit(ctx context.Context, printerName, jobName string, ticket model.JobTicket, doc io.Reader) (spoolerID string, err error)
	JobState(spoolerID string) (model.JobState, bool)
	Cancel(ctx context.Context, spoolerID string) error
}

// Server is the CUPS-compatible IPP front end.
type Server struct {
	backend Backend

	// Logf, if set, receives one line per request (the operation and outcome).
	// The daemon wires it to its logger; it defaults to a no-op.
	Logf func(format string, args ...any)

	mu      sync.Mutex
	counter int
	jobs    map[int]*compatJob // CUPS integer job-id -> job
}

// compatJob bridges a CUPS integer job-id to a Torchio spooler job. For the
// Create-Job/Send-Document flow the document is accumulated until the last one.
type compatJob struct {
	id        int
	printer   string
	name      string
	ticket    model.JobTicket
	buf       bytes.Buffer
	spoolerID string
}

// New builds a compat server over the backend.
func New(b Backend) *Server {
	return &Server{backend: b, jobs: map[int]*compatJob{}, Logf: func(string, ...any) {}}
}

// ServeHTTP implements the CUPS IPP endpoint. It dispatches on the IPP
// operation code, not the URL path, so it works for any path libcups posts to.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// A GET for /printers/NAME.ppd is a client (Chromium) asking for the
		// queue's driver file. Our queues are driverless, so we synthesize a PPD
		// from the printer's IPP capabilities, like CUPS does for its temporary
		// queues. Without this the printer is dropped from the print dialog.
		if name, ok := ppdQueueName(r.URL.Path); ok {
			p, found := s.backend.PrinterByName(name)
			if !found {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.cups-ppd")
			_, _ = io.WriteString(w, generatePPD(p))
			s.Logf("served PPD for %s", name)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	body := bufio.NewReader(r.Body)
	var req goipp.Message
	if err := req.Decode(body); err != nil {
		http.Error(w, "bad IPP: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, req.RequestID)
	opAttrs(resp)

	op := goipp.Op(req.Code)
	switch op {
	case goipp.OpCupsGetDefault:
		s.cupsGetDefault(resp)
	case goipp.OpCupsGetPrinters:
		s.cupsGetPrinters(resp)
	case goipp.OpGetPrinterAttributes:
		s.getPrinterAttributes(req, resp)
	case goipp.OpValidateJob:
		// accept
	case goipp.OpPrintJob:
		s.printJob(r.Context(), req, body, resp)
	case goipp.OpCreateJob:
		s.createJob(req, resp)
	case goipp.OpSendDocument:
		s.sendDocument(r.Context(), req, body, resp)
	case goipp.OpGetJobAttributes:
		s.getJobAttributes(req, resp)
	case goipp.OpCancelJob:
		s.cancelJob(r.Context(), req, resp)
	case goipp.OpGetJobs:
		s.getJobs(resp)
	default:
		resp.Code = goipp.Code(goipp.StatusErrorOperationNotSupported)
	}
	s.Logf("op=%s status=%s", op, goipp.Status(resp.Code))

	payload, err := resp.EncodeBytes()
	if err != nil {
		http.Error(w, "encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", goipp.ContentType)
	_, _ = w.Write(payload)
}

func (s *Server) cupsGetDefault(resp *goipp.Message) {
	p, ok := s.backend.DefaultPrinter()
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	resp.Printer = printerAttrs(p)
}

// cupsGetPrinters returns one printer group per configured printer. libcups
// (cupsGetDests) parses these into the destinations every app shows.
func (s *Server) cupsGetPrinters(resp *goipp.Message) {
	printers := s.backend.ListPrinters()
	groups := goipp.Groups{{Tag: goipp.TagOperationGroup, Attrs: resp.Operation}}
	for _, p := range printers {
		groups = append(groups, goipp.Group{Tag: goipp.TagPrinterGroup, Attrs: printerAttrs(p)})
	}
	resp.Groups = groups // overrides the legacy fields when encoding
}

func (s *Server) getPrinterAttributes(req goipp.Message, resp *goipp.Message) {
	p, ok := s.resolvePrinter(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	resp.Printer = printerAttrs(p)
}

func (s *Server) printJob(ctx context.Context, req goipp.Message, doc io.Reader, resp *goipp.Message) {
	p, ok := s.resolvePrinter(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	j := s.newJob(p.Name, ticketFrom(req), jobName(req))
	data, _ := io.ReadAll(doc)
	if err := s.submit(ctx, j, bytes.NewReader(data)); err != nil {
		s.fail(resp, err)
		return
	}
	s.jobReply(resp, j, model.JobPending)
}

func (s *Server) createJob(req goipp.Message, resp *goipp.Message) {
	p, ok := s.resolvePrinter(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	j := s.newJob(p.Name, ticketFrom(req), jobName(req))
	s.jobReply(resp, j, model.JobPending)
}

func (s *Server) sendDocument(ctx context.Context, req goipp.Message, doc io.Reader, resp *goipp.Message) {
	id, _ := intAttr(req.Operation, "job-id")
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j == nil {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	if _, err := io.Copy(&j.buf, doc); err != nil {
		s.fail(resp, err)
		return
	}
	last, _ := boolAttr(req.Operation, "last-document")
	if last && j.buf.Len() > 0 {
		if err := s.submit(ctx, j, bytes.NewReader(j.buf.Bytes())); err != nil {
			s.fail(resp, err)
			return
		}
	}
	s.jobReply(resp, j, model.JobPending)
}

func (s *Server) getJobAttributes(req goipp.Message, resp *goipp.Message) {
	id, _ := intAttr(req.Operation, "job-id")
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j == nil {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	s.jobReply(resp, j, s.stateOf(j))
}

func (s *Server) cancelJob(ctx context.Context, req goipp.Message, resp *goipp.Message) {
	id, _ := intAttr(req.Operation, "job-id")
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j == nil {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	if j.spoolerID != "" {
		_ = s.backend.Cancel(ctx, j.spoolerID)
	}
}

func (s *Server) getJobs(resp *goipp.Message) {
	s.mu.Lock()
	jobs := make([]*compatJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.Unlock()

	groups := goipp.Groups{{Tag: goipp.TagOperationGroup, Attrs: resp.Operation}}
	for _, j := range jobs {
		groups = append(groups, goipp.Group{Tag: goipp.TagJobGroup, Attrs: jobAttrs(j, s.stateOf(j))})
	}
	resp.Groups = groups
}

// --- job bookkeeping ---

func (s *Server) newJob(printer string, ticket model.JobTicket, name string) *compatJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	j := &compatJob{id: s.counter, printer: printer, ticket: ticket, name: name}
	s.jobs[j.id] = j
	return j
}

func (s *Server) submit(ctx context.Context, j *compatJob, doc io.Reader) error {
	id, err := s.backend.Submit(ctx, j.printer, j.name, j.ticket, doc)
	if err != nil {
		return err
	}
	s.mu.Lock()
	j.spoolerID = id
	s.mu.Unlock()
	return nil
}

func (s *Server) stateOf(j *compatJob) model.JobState {
	s.mu.Lock()
	id := j.spoolerID
	s.mu.Unlock()
	if id == "" {
		return model.JobPending
	}
	if st, ok := s.backend.JobState(id); ok {
		return st
	}
	return model.JobPending
}

func (s *Server) jobReply(resp *goipp.Message, j *compatJob, state model.JobState) {
	resp.Job = jobAttrs(j, state)
}

func (s *Server) fail(resp *goipp.Message, err error) {
	resp.Code = goipp.Code(goipp.StatusErrorInternal)
	resp.Operation.Add(goipp.MakeAttribute("status-message", goipp.TagText, goipp.String(err.Error())))
}

// resolvePrinter finds the target printer from the request: the queue name in
// the printer-uri path (ipp://host/printers/NAME), or a printer-name attr, or
// the default.
func (s *Server) resolvePrinter(op goipp.Attributes) (model.Printer, bool) {
	if uri, ok := strAttr(op, "printer-uri"); ok {
		if name := queueName(uri); name != "" {
			if p, ok := s.backend.PrinterByName(name); ok {
				return p, true
			}
		}
	}
	if name, ok := strAttr(op, "printer-name"); ok {
		if p, ok := s.backend.PrinterByName(name); ok {
			return p, true
		}
	}
	return s.backend.DefaultPrinter()
}

// queueName extracts the printer name from a CUPS queue URI's path.
func queueName(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	p := strings.TrimPrefix(u.Path, "/")
	for _, pfx := range []string{"printers/", "classes/"} {
		if strings.HasPrefix(p, pfx) {
			return strings.TrimPrefix(p, pfx)
		}
	}
	return ""
}

// Idle reports whether any compat jobs are still active (used by graceful
// shutdown decisions; cheap and best-effort).
func (s *Server) Idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if !s.spoolerTerminal(j) {
			return false
		}
	}
	return true
}

func (s *Server) spoolerTerminal(j *compatJob) bool {
	if j.spoolerID == "" {
		return false
	}
	st, ok := s.backend.JobState(j.spoolerID)
	return ok && st.IsTerminal()
}
