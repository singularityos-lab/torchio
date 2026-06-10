package host

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/model"
)

// IPP job-state enum values.
const (
	jobStateProcessing = 5
	jobStateCanceled   = 7
	jobStateAborted    = 8
	jobStateCompleted  = 9
)

// SubmitFunc runs a received document through the driver and out to the device,
// returning when the job is done. It is injected by the host (or a test).
type SubmitFunc func(ctx context.Context, format string, doc io.Reader) error

// Server is the printer side of IPP: an HTTP handler that answers
// Get-Printer-Attributes from a driver's capabilities and runs Print-Job
// through the driver. It turns a hosted driver into an ordinary IPP printer.
type Server struct {
	name    string
	caps    model.Capabilities
	primary model.MIME
	submit  SubmitFunc

	mu      sync.Mutex
	jobs    map[int]int // job-id -> job-state
	counter int
}

// NewServer builds an IPP server advertising caps and running submit per job.
func NewServer(name string, caps model.Capabilities, primary model.MIME, submit SubmitFunc) *Server {
	return &Server{name: name, caps: caps, primary: primary, submit: submit, jobs: map[int]int{}}
}

// ServeHTTP implements the printer-side IPP over HTTP.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "IPP requires POST", http.StatusMethodNotAllowed)
		return
	}

	body := bufio.NewReader(r.Body)
	var req goipp.Message
	if err := req.Decode(body); err != nil {
		http.Error(w, "bad IPP request: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, req.RequestID)
	addOperationDefaults(resp)

	switch goipp.Op(req.Code) {
	case goipp.OpGetPrinterAttributes:
		s.handleGetPrinterAttributes(resp, r.Host)
	case goipp.OpValidateJob:
		// Accept any job whose format we can consume; the client already chose.
	case goipp.OpPrintJob:
		s.handlePrintJob(r.Context(), req, body, resp)
	case goipp.OpGetJobAttributes:
		s.handleGetJobAttributes(req, resp)
	case goipp.OpCancelJob:
		s.handleCancelJob(req, resp)
	default:
		resp.Code = goipp.Code(goipp.StatusErrorOperationNotSupported)
	}

	payload, err := resp.EncodeBytes()
	if err != nil {
		http.Error(w, "encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", goipp.ContentType)
	_, _ = w.Write(payload)
}

func (s *Server) handleGetPrinterAttributes(resp *goipp.Message, host string) {
	p := &resp.Printer
	uri := "ipp://" + host + "/ipp/print"

	add := func(name string, tag goipp.Tag, vals ...goipp.Value) {
		if len(vals) == 0 {
			return
		}
		p.Add(goipp.MakeAttr(name, tag, vals[0], vals[1:]...))
	}

	add("printer-uri-supported", goipp.TagURI, goipp.String(uri))
	add("uri-authentication-supported", goipp.TagKeyword, goipp.String("none"))
	add("uri-security-supported", goipp.TagKeyword, goipp.String("none"))
	add("printer-name", goipp.TagName, goipp.String(s.name))
	add("printer-make-and-model", goipp.TagText, goipp.String(s.caps.MakeAndModel))
	add("printer-state", goipp.TagEnum, goipp.Integer(s.caps.State))
	add("printer-state-reasons", goipp.TagKeyword, goipp.String("none"))
	add("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(true))
	add("ipp-versions-supported", goipp.TagKeyword, goipp.String("1.1"), goipp.String("2.0"))
	add("charset-configured", goipp.TagCharset, goipp.String("utf-8"))
	add("charset-supported", goipp.TagCharset, goipp.String("utf-8"))
	add("natural-language-configured", goipp.TagLanguage, goipp.String("en-us"))
	add("generated-natural-language-supported", goipp.TagLanguage, goipp.String("en-us"))
	add("document-format-default", goipp.TagMimeType, goipp.String(s.primary.String()))
	add("compression-supported", goipp.TagKeyword, goipp.String("none"))
	add("pdl-override-supported", goipp.TagKeyword, goipp.String("attempted"))

	ops := []goipp.Value{
		goipp.Integer(goipp.OpPrintJob), goipp.Integer(goipp.OpValidateJob),
		goipp.Integer(goipp.OpGetPrinterAttributes), goipp.Integer(goipp.OpGetJobAttributes),
		goipp.Integer(goipp.OpCancelJob),
	}
	p.Add(goipp.MakeAttr("operations-supported", goipp.TagEnum, ops[0], ops[1:]...))

	// Capability attributes (the ones ipp.ParseCapabilities reads).
	if formats := stringValues(mimeStrings(s.caps.DocumentFormats)); len(formats) > 0 {
		p.Add(goipp.MakeAttr("document-format-supported", goipp.TagMimeType, formats[0], formats[1:]...))
	}
	addKeywordSet(p, "media-supported", s.caps.Media)
	addKeywordSet(p, "media-default", first(s.caps.Media))
	addKeywordSet(p, "sides-supported", s.caps.Sides)
	addKeywordSet(p, "sides-default", first(s.caps.Sides))
	addKeywordSet(p, "print-color-mode-supported", s.caps.ColorModes)
	if res := resolutionValues(s.caps.Resolutions); len(res) > 0 {
		p.Add(goipp.MakeAttr("printer-resolution-supported", goipp.TagResolution, res[0], res[1:]...))
	}

	s.mu.Lock()
	add("queued-job-count", goipp.TagInteger, goipp.Integer(s.activeLocked()))
	s.mu.Unlock()
}

func (s *Server) handlePrintJob(ctx context.Context, req goipp.Message, doc io.Reader, resp *goipp.Message) {
	format := s.primary.String()
	if f, ok := firstString(req.Operation, "document-format"); ok {
		format = f
	}

	s.mu.Lock()
	s.counter++
	id := s.counter
	s.jobs[id] = jobStateProcessing
	s.mu.Unlock()

	if err := s.submit(ctx, format, doc); err != nil {
		s.setJobState(id, jobStateAborted)
		resp.Code = goipp.Code(goipp.StatusErrorInternal)
		resp.Operation.Add(goipp.MakeAttribute("status-message", goipp.TagText, goipp.String(err.Error())))
		return
	}
	s.setJobState(id, jobStateCompleted)

	resp.Job.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	resp.Job.Add(goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String(fmt.Sprintf("ipp://localhost/jobs/%d", id))))
	resp.Job.Add(goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(jobStateCompleted)))
	resp.Job.Add(goipp.MakeAttribute("job-state-reasons", goipp.TagKeyword, goipp.String("none")))
}

func (s *Server) handleGetJobAttributes(req goipp.Message, resp *goipp.Message) {
	id, _ := firstInt(req.Operation, "job-id")
	s.mu.Lock()
	state, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
		return
	}
	resp.Job.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	resp.Job.Add(goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(state)))
}

func (s *Server) handleCancelJob(req goipp.Message, resp *goipp.Message) {
	id, _ := firstInt(req.Operation, "job-id")
	s.mu.Lock()
	state, ok := s.jobs[id]
	if ok && state != jobStateCompleted {
		s.jobs[id] = jobStateCanceled
	}
	s.mu.Unlock()
	if !ok {
		resp.Code = goipp.Code(goipp.StatusErrorNotFound)
	}
}

func (s *Server) setJobState(id, state int) {
	s.mu.Lock()
	s.jobs[id] = state
	s.mu.Unlock()
}

func (s *Server) activeLocked() int {
	n := 0
	for _, st := range s.jobs {
		if st == jobStateProcessing {
			n++
		}
	}
	return n
}

// --- small attribute helpers ---

func addOperationDefaults(m *goipp.Message) {
	m.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	m.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
}

func addKeywordSet(attrs *goipp.Attributes, name string, vals []string) {
	v := stringValues(vals)
	if len(v) == 0 {
		return
	}
	attrs.Add(goipp.MakeAttr(name, goipp.TagKeyword, v[0], v[1:]...))
}

func stringValues(ss []string) []goipp.Value {
	out := make([]goipp.Value, 0, len(ss))
	for _, s := range ss {
		out = append(out, goipp.String(s))
	}
	return out
}

func mimeStrings(ms []model.MIME) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.String()
	}
	return out
}

func first(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	return ss[:1]
}

// resolutionValues parses "600dpi" / "600x300dpi" strings into IPP resolutions.
func resolutionValues(res []string) []goipp.Value {
	var out []goipp.Value
	for _, r := range res {
		s := strings.TrimSuffix(strings.TrimSuffix(r, "dpi"), "dpcm")
		x, y := s, s
		if i := strings.IndexByte(s, 'x'); i >= 0 {
			x, y = s[:i], s[i+1:]
		}
		xi, err1 := strconv.Atoi(x)
		yi, err2 := strconv.Atoi(y)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, goipp.Resolution{Xres: xi, Yres: yi, Units: goipp.UnitsDpi})
	}
	return out
}

// firstString/firstInt read a value from an operation group (server side).
func firstString(attrs goipp.Attributes, name string) (string, bool) {
	for _, a := range attrs {
		if a.Name == name && len(a.Values) > 0 {
			if s, ok := a.Values[0].V.(goipp.String); ok {
				return string(s), true
			}
		}
	}
	return "", false
}

func firstInt(attrs goipp.Attributes, name string) (int, bool) {
	for _, a := range attrs {
		if a.Name == name && len(a.Values) > 0 {
			if i, ok := a.Values[0].V.(goipp.Integer); ok {
				return int(i), true
			}
		}
	}
	return 0, false
}
