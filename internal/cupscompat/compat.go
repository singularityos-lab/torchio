// Package cupscompat makes Torchio a drop-in for CUPS: it answers the IPP
// dialect libcups speaks on the CUPS socket, including the CUPS extensions for
// listing, adding and managing queues, so GTK and Qt print dialogs, lp/lpstat,
// Chromium and printer settings panels work against Torchio's queues. cupsd
// must not own the socket.
package cupscompat

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/model"
)

// Job is a spooler job as CUPS clients see it.
type Job struct {
	Number  int
	Printer string
	Name    string
	State   model.JobState
	Reason  string
	Size    int64
	Created time.Time
	Updated time.Time
}

// Device is a printer the client may add, as reported by CUPS-Get-Devices.
type Device struct {
	URI       string
	Info      string
	MakeModel string
	Location  string
	Class     string
}

// PrinterChange is a CUPS-Add-Modify-Printer request. Nil fields are left
// unchanged.
type PrinterChange struct {
	URI       *string
	Info      *string
	Location  *string
	Shared    *bool
	Accepting *bool
	Paused    *bool
	// Message is shown while the queue is paused.
	Message  *string
	Defaults model.JobTicket
}

// Job actions accepted by Backend.JobAction.
const (
	ActionCancel  = "cancel"
	ActionHold    = "hold"
	ActionRelease = "release"
	ActionRestart = "restart"
)

// Which-jobs filters accepted by Backend.Jobs.
const (
	WhichActive   = "not-completed"
	WhichFinished = "completed"
	WhichAll      = "all"
)

// ErrNotFound tells the server to answer client-error-not-found.
var ErrNotFound = errors.New("cupscompat: not found")

// ErrNotPossible tells the server to answer client-error-not-possible.
var ErrNotPossible = errors.New("cupscompat: not possible")

// Backend is the slice of Torchio the compat server needs. The daemon
// implements it over the store, the queue engine and the trust backend.
type Backend interface {
	ListPrinters() []model.Printer
	DefaultPrinter() (model.Printer, bool)
	PrinterByName(name string) (model.Printer, bool)
	Submit(ctx context.Context, printer, jobName string, ticket model.JobTicket, doc io.Reader) (Job, error)
	// Create records a job whose document arrives later through Attach.
	Create(ctx context.Context, printer, jobName string, ticket model.JobTicket) (Job, error)
	Attach(ctx context.Context, number int, doc io.Reader) (Job, error)
	Job(number int) (Job, bool)
	// Jobs lists jobs of one printer, or of every printer when printer is "".
	Jobs(printer, which string) []Job
	JobAction(ctx context.Context, number int, action string) error
	// AddOrModifyPrinter creates the queue when it does not exist.
	AddOrModifyPrinter(ctx context.Context, name string, c PrinterChange) error
	DeletePrinter(name string) error
	SetDefault(name string) error
	Devices(ctx context.Context) []Device
}

// Server is the CUPS-compatible IPP front end.
type Server struct {
	backend Backend

	// Logf, if set, receives one line per request.
	Logf func(format string, args ...any)

	mu sync.Mutex
	// incoming collects the documents of a Create-Job job until the last one.
	incoming map[int]*bytes.Buffer
}

// New builds a compat server over the backend.
func New(b Backend) *Server {
	return &Server{backend: b, incoming: map[int]*bytes.Buffer{}, Logf: func(string, ...any) {}}
}

// ServeHTTP implements the CUPS IPP endpoint. It dispatches on the IPP
// operation code, not the URL path, so it works for any path libcups posts to.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Chromium fetches /printers/NAME.ppd and drops a queue without one.
		// Our queues are driverless, so the PPD is generated from the
		// printer's IPP capabilities, like CUPS does for temporary queues.
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

	ctx := r.Context()
	op := goipp.Op(req.Code)
	switch op {
	case goipp.OpCupsGetDefault:
		s.cupsGetDefault(resp)
	case goipp.OpCupsGetPrinters:
		s.cupsGetPrinters(resp)
	case goipp.OpGetPrinterAttributes:
		s.getPrinterAttributes(req, resp)
	case goipp.OpValidateJob:
		s.validateJob(req, resp)
	case goipp.OpPrintJob:
		s.printJob(ctx, req, body, resp)
	case goipp.OpCreateJob:
		s.createJob(ctx, req, resp)
	case goipp.OpSendDocument:
		s.sendDocument(ctx, req, body, resp)
	case goipp.OpGetJobAttributes:
		s.getJobAttributes(req, resp)
	case goipp.OpGetJobs:
		s.getJobs(req, resp)
	case goipp.OpCancelJob:
		s.jobAction(ctx, req, resp, ActionCancel)
	case goipp.OpHoldJob:
		s.jobAction(ctx, req, resp, ActionHold)
	case goipp.OpReleaseJob:
		s.jobAction(ctx, req, resp, ActionRelease)
	case goipp.OpRestartJob:
		s.jobAction(ctx, req, resp, ActionRestart)
	case goipp.OpSetJobAttributes:
		s.setJobAttributes(ctx, req, resp)
	case goipp.OpPurgeJobs, goipp.OpCancelJobs, goipp.OpCancelMyJobs:
		s.cancelAll(ctx, req, resp)
	case goipp.OpPausePrinter:
		reason, _ := strAttr(req.Operation, "printer-state-message")
		s.change(ctx, req, resp, PrinterChange{Paused: ptr(true), Message: &reason})
	case goipp.OpResumePrinter:
		s.change(ctx, req, resp, PrinterChange{Paused: ptr(false)})
	case goipp.OpCupsAcceptJobs:
		s.change(ctx, req, resp, PrinterChange{Accepting: ptr(true)})
	case goipp.OpCupsRejectJobs:
		s.change(ctx, req, resp, PrinterChange{Accepting: ptr(false)})
	case goipp.OpCupsAddModifyPrinter:
		s.addModifyPrinter(ctx, req, resp)
	case goipp.OpCupsDeletePrinter:
		s.deletePrinter(req, resp)
	case goipp.OpCupsSetDefault:
		s.setDefault(req, resp)
	case goipp.OpCupsGetDevices:
		s.getDevices(ctx, resp)
	case goipp.OpCupsGetPpds:
		s.getPPDs(resp)
	case goipp.OpCupsGetClasses:
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
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

func ptr[T any](v T) *T { return &v }

func (s *Server) cupsGetDefault(resp *goipp.Message) {
	p, ok := s.backend.DefaultPrinter()
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	resp.Printer = s.printerAttrs(p)
}

// cupsGetPrinters returns one printer group per configured printer. libcups
// (cupsGetDests) parses these into the destinations every app shows.
func (s *Server) cupsGetPrinters(resp *goipp.Message) {
	groups := goipp.Groups{{Tag: goipp.TagOperationGroup, Attrs: resp.Operation}}
	for _, p := range s.backend.ListPrinters() {
		groups = append(groups, goipp.Group{Tag: goipp.TagPrinterGroup, Attrs: s.printerAttrs(p)})
	}
	resp.Groups = groups
}

func (s *Server) getPrinterAttributes(req goipp.Message, resp *goipp.Message) {
	p, ok := s.resolvePrinter(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	resp.Printer = s.printerAttrs(p)
}

func (s *Server) printerAttrs(p model.Printer) goipp.Attributes {
	return printerAttrs(p, len(s.backend.Jobs(p.Name, WhichActive)))
}

func (s *Server) validateJob(req goipp.Message, resp *goipp.Message) {
	p, ok := s.resolvePrinter(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	if p.Queue.Rejecting {
		resp.Code = goipp.Code(goipp.StatusErrorNotAcceptingJobs)
	}
}

func (s *Server) printJob(ctx context.Context, req goipp.Message, doc io.Reader, resp *goipp.Message) {
	p, ok := s.resolvePrinter(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	job, err := s.backend.Submit(ctx, p.Name, jobName(req), ticketFrom(req), doc)
	if err != nil {
		s.fail(resp, err)
		return
	}
	resp.Job = jobAttrs(job)
}

func (s *Server) createJob(ctx context.Context, req goipp.Message, resp *goipp.Message) {
	p, ok := s.resolvePrinter(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	job, err := s.backend.Create(ctx, p.Name, jobName(req), ticketFrom(req))
	if err != nil {
		s.fail(resp, err)
		return
	}
	s.mu.Lock()
	s.incoming[job.Number] = &bytes.Buffer{}
	s.mu.Unlock()
	job.Reason = "job-incoming"
	resp.Job = jobAttrs(job)
}

func (s *Server) sendDocument(ctx context.Context, req goipp.Message, doc io.Reader, resp *goipp.Message) {
	id, _ := intAttr(req.Operation, "job-id")
	s.mu.Lock()
	buf := s.incoming[id]
	s.mu.Unlock()
	if buf == nil {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	if _, err := io.Copy(buf, doc); err != nil {
		s.fail(resp, err)
		return
	}
	if last, _ := boolAttr(req.Operation, "last-document"); !last {
		if job, ok := s.backend.Job(id); ok {
			job.Reason = "job-incoming"
			resp.Job = jobAttrs(job)
		}
		return
	}
	s.mu.Lock()
	delete(s.incoming, id)
	s.mu.Unlock()
	job, err := s.backend.Attach(ctx, id, buf)
	if err != nil {
		s.fail(resp, err)
		return
	}
	resp.Job = jobAttrs(job)
}

// lookup resolves the job a request is about.
func (s *Server) lookup(op goipp.Attributes) (Job, bool) {
	id, ok := intAttr(op, "job-id")
	if !ok {
		if uri, found := strAttr(op, "job-uri"); found {
			id = jobNumber(uri)
		}
	}
	job, found := s.backend.Job(id)
	if !found {
		return Job{}, false
	}
	s.mu.Lock()
	_, incoming := s.incoming[id]
	s.mu.Unlock()
	if incoming {
		job.Reason = "job-incoming"
	}
	return job, true
}

func (s *Server) getJobAttributes(req goipp.Message, resp *goipp.Message) {
	job, ok := s.lookup(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	resp.Job = jobAttrs(job)
}

func (s *Server) getJobs(req goipp.Message, resp *goipp.Message) {
	which := WhichActive
	if v, ok := strAttr(req.Operation, "which-jobs"); ok {
		switch v {
		case "completed":
			which = WhichFinished
		case "all":
			which = WhichAll
		}
	}
	printer := ""
	if uri, ok := strAttr(req.Operation, "printer-uri"); ok {
		printer = queueName(uri)
	}
	limit, _ := intAttr(req.Operation, "limit")

	jobs := s.backend.Jobs(printer, which)
	groups := goipp.Groups{{Tag: goipp.TagOperationGroup, Attrs: resp.Operation}}
	for i, j := range jobs {
		if limit > 0 && i >= limit {
			break
		}
		groups = append(groups, goipp.Group{Tag: goipp.TagJobGroup, Attrs: jobAttrs(j)})
	}
	resp.Groups = groups
}

func (s *Server) jobAction(ctx context.Context, req goipp.Message, resp *goipp.Message, action string) {
	job, ok := s.lookup(req.Operation)
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	if action == ActionCancel {
		s.mu.Lock()
		delete(s.incoming, job.Number)
		s.mu.Unlock()
	}
	if err := s.backend.JobAction(ctx, job.Number, action); err != nil {
		s.fail(resp, err)
	}
}

// setJobAttributes handles the hold and release requests of lp -H.
func (s *Server) setJobAttributes(ctx context.Context, req goipp.Message, resp *goipp.Message) {
	hold, ok := strAttr(req.Job, "job-hold-until")
	if !ok {
		return
	}
	action := ActionHold
	if hold == "no-hold" {
		action = ActionRelease
	}
	s.jobAction(ctx, req, resp, action)
}

func (s *Server) cancelAll(ctx context.Context, req goipp.Message, resp *goipp.Message) {
	printer := ""
	if uri, ok := strAttr(req.Operation, "printer-uri"); ok {
		printer = queueName(uri)
	}
	for _, j := range s.backend.Jobs(printer, WhichActive) {
		_ = s.backend.JobAction(ctx, j.Number, ActionCancel)
	}
}

func (s *Server) change(ctx context.Context, req goipp.Message, resp *goipp.Message, c PrinterChange) {
	name := s.targetName(req.Operation)
	if _, ok := s.backend.PrinterByName(name); !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	if err := s.backend.AddOrModifyPrinter(ctx, name, c); err != nil {
		s.fail(resp, err)
	}
}

func (s *Server) addModifyPrinter(ctx context.Context, req goipp.Message, resp *goipp.Message) {
	name := s.targetName(req.Operation)
	if name == "" {
		resp.Code = goipp.Code(goipp.StatusErrorBadRequest)
		return
	}
	attrs := req.Printer
	c := PrinterChange{}
	if v, ok := strAttr(attrs, "device-uri"); ok {
		c.URI = &v
	}
	if v, ok := strAttr(attrs, "printer-info"); ok {
		c.Info = &v
	}
	if v, ok := strAttr(attrs, "printer-location"); ok {
		c.Location = &v
	}
	if v, ok := boolAttr(attrs, "printer-is-shared"); ok {
		c.Shared = &v
	}
	if v, ok := boolAttr(attrs, "printer-is-accepting-jobs"); ok {
		c.Accepting = &v
	}
	if v, ok := intAttr(attrs, "printer-state"); ok {
		c.Paused = ptr(v == 5)
	}
	c.Defaults.Media, _ = strAttr(attrs, "media-default")
	c.Defaults.Sides, _ = strAttr(attrs, "sides-default")
	c.Defaults.Color, _ = strAttr(attrs, "print-color-mode-default")
	c.Defaults.Quality, _ = intAttr(attrs, "print-quality-default")
	c.Defaults.Copies, _ = intAttr(attrs, "copies-default")
	if err := s.backend.AddOrModifyPrinter(ctx, name, c); err != nil {
		s.fail(resp, err)
	}
}

func (s *Server) deletePrinter(req goipp.Message, resp *goipp.Message) {
	if err := s.backend.DeletePrinter(s.targetName(req.Operation)); err != nil {
		s.fail(resp, err)
	}
}

func (s *Server) setDefault(req goipp.Message, resp *goipp.Message) {
	if err := s.backend.SetDefault(s.targetName(req.Operation)); err != nil {
		s.fail(resp, err)
	}
}

func (s *Server) getDevices(ctx context.Context, resp *goipp.Message) {
	groups := goipp.Groups{{Tag: goipp.TagOperationGroup, Attrs: resp.Operation}}
	for _, d := range s.backend.Devices(ctx) {
		var a goipp.Attributes
		a.Add(goipp.MakeAttribute("device-class", goipp.TagKeyword, goipp.String(d.Class)))
		a.Add(goipp.MakeAttribute("device-info", goipp.TagText, goipp.String(d.Info)))
		a.Add(goipp.MakeAttribute("device-make-and-model", goipp.TagText, goipp.String(d.MakeModel)))
		a.Add(goipp.MakeAttribute("device-uri", goipp.TagURI, goipp.String(d.URI)))
		a.Add(goipp.MakeAttribute("device-id", goipp.TagText, goipp.String("")))
		a.Add(goipp.MakeAttribute("device-location", goipp.TagText, goipp.String(d.Location)))
		groups = append(groups, goipp.Group{Tag: goipp.TagPrinterGroup, Attrs: a})
	}
	resp.Groups = groups
}

// getPPDs offers the single "driver" every Torchio queue uses.
func (s *Server) getPPDs(resp *goipp.Message) {
	var a goipp.Attributes
	a.Add(goipp.MakeAttribute("ppd-name", goipp.TagName, goipp.String("everywhere")))
	a.Add(goipp.MakeAttribute("ppd-make", goipp.TagText, goipp.String("Generic")))
	a.Add(goipp.MakeAttribute("ppd-make-and-model", goipp.TagText, goipp.String("IPP Everywhere")))
	a.Add(goipp.MakeAttribute("ppd-natural-language", goipp.TagLanguage, goipp.String("en")))
	resp.Groups = goipp.Groups{
		{Tag: goipp.TagOperationGroup, Attrs: resp.Operation},
		{Tag: goipp.TagPrinterGroup, Attrs: a},
	}
}

func (s *Server) fail(resp *goipp.Message, err error) {
	status := goipp.StatusErrorInternal
	switch {
	case errors.Is(err, ErrNotFound):
		status = goipp.StatusErrorNotFound
	case errors.Is(err, ErrNotPossible):
		status = goipp.StatusErrorNotPossible
	}
	resp.Code = goipp.Code(status)
	resp.Operation.Add(goipp.MakeAttribute("status-message", goipp.TagText, goipp.String(err.Error())))
}

// targetName is the queue an administrative request is about.
func (s *Server) targetName(op goipp.Attributes) string {
	if uri, ok := strAttr(op, "printer-uri"); ok {
		if name := queueName(uri); name != "" {
			return name
		}
	}
	name, _ := strAttr(op, "printer-name")
	return name
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
			name, _ := url.PathUnescape(strings.TrimPrefix(p, pfx))
			return name
		}
	}
	return ""
}

// jobNumber extracts the job id from ipp://localhost/jobs/N.
func jobNumber(uri string) int {
	u, err := url.Parse(uri)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range strings.TrimPrefix(u.Path, "/jobs/") {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
