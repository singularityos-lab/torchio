// Package fakeprinter is a real, inspectable IPP printer for testing Torchio
// end to end. It behaves like an IPP Everywhere printer (answers the standard
// operations) but is fully observable: it records every job, detects the actual
// format of the bytes it received (so we can confirm, say, that a PDF reached a
// raster-only printer as PWG raster), and optionally saves each document to
// disk. Its supported formats and capabilities are configurable, so one printer
// can force the PDF fast path and another can force rasterization.
package fakeprinter

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/detect"
	"github.com/singularityos-lab/torchio/internal/model"
)

// Config configures a fake printer.
type Config struct {
	Name        string
	Formats     []model.MIME // document-format-supported
	Media       []string
	Sides       []string
	Resolutions []string
	Colors      []string
	State       int    // printer-state enum (3 idle)
	SaveDir     string // if set, received documents are written here
	Logf        func(format string, args ...any)
}

// ReceivedJob is what the printer observed for one print job.
type ReceivedJob struct {
	ID             int
	DeclaredFormat string         // document-format from the client
	DetectedFormat model.MIME     // what the bytes actually are
	Size           int64          // document byte count
	Ticket         map[string]any // copies, sides, media, ...
	SavedPath      string         // where the document was written, if any
}

// Printer is the fake IPP printer.
type Printer struct {
	cfg Config

	mu      sync.Mutex
	jobs    []ReceivedJob
	counter int
}

// New builds a fake printer.
func New(cfg Config) *Printer {
	if cfg.Name == "" {
		cfg.Name = "Torchio Fake Printer"
	}
	if cfg.State == 0 {
		cfg.State = 3
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Printer{cfg: cfg}
}

// Jobs returns a copy of the jobs received so far.
func (p *Printer) Jobs() []ReceivedJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ReceivedJob(nil), p.jobs...)
}

// ServeHTTP answers IPP over HTTP.
func (p *Printer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "IPP requires POST", http.StatusMethodNotAllowed)
		return
	}
	body := bufio.NewReader(r.Body)
	var req goipp.Message
	if err := req.Decode(body); err != nil {
		http.Error(w, "bad IPP: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, req.RequestID)
	resp.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	resp.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))

	switch goipp.Op(req.Code) {
	case goipp.OpGetPrinterAttributes:
		p.getPrinterAttributes(resp, r.Host)
	case goipp.OpValidateJob:
		// accept
	case goipp.OpPrintJob:
		p.printJob(req, body, resp)
	case goipp.OpGetJobAttributes:
		p.getJobAttributes(req, resp)
	case goipp.OpCancelJob:
		// accept, nothing queued long enough to cancel
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

func (p *Printer) printJob(req goipp.Message, doc io.Reader, resp *goipp.Message) {
	declared := strFromGroup(req.Operation, "document-format")
	data, _ := io.ReadAll(doc)
	detected := detect.FormatBytes(data)

	p.mu.Lock()
	p.counter++
	id := p.counter
	p.mu.Unlock()

	ticket := map[string]any{}
	if v, ok := intFromGroup(req.Job, "copies"); ok {
		ticket["copies"] = v
	}
	if v := strFromGroup(req.Job, "sides"); v != "" {
		ticket["sides"] = v
	}
	if v := strFromGroup(req.Job, "media"); v != "" {
		ticket["media"] = v
	}

	saved := ""
	if p.cfg.SaveDir != "" {
		if err := os.MkdirAll(p.cfg.SaveDir, 0o755); err == nil {
			saved = filepath.Join(p.cfg.SaveDir, fmt.Sprintf("job-%03d%s", id, extFor(detected)))
			_ = os.WriteFile(saved, data, 0o644)
		}
	}

	job := ReceivedJob{
		ID:             id,
		DeclaredFormat: declared,
		DetectedFormat: detected,
		Size:           int64(len(data)),
		Ticket:         ticket,
		SavedPath:      saved,
	}
	p.mu.Lock()
	p.jobs = append(p.jobs, job)
	p.mu.Unlock()

	p.cfg.Logf("job %d: declared=%s detected=%s size=%d ticket=%v saved=%s",
		id, orDash(declared), detected, job.Size, ticket, orDash(saved))

	resp.Job.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	resp.Job.Add(goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String(fmt.Sprintf("ipp://localhost/jobs/%d", id))))
	resp.Job.Add(goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(9))) // completed
	resp.Job.Add(goipp.MakeAttribute("job-state-reasons", goipp.TagKeyword, goipp.String("none")))
}

func (p *Printer) getJobAttributes(req goipp.Message, resp *goipp.Message) {
	id, _ := intFromGroup(req.Operation, "job-id")
	resp.Job.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	resp.Job.Add(goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(9)))
}

func (p *Printer) getPrinterAttributes(resp *goipp.Message, host string) {
	a := &resp.Printer
	add := func(name string, tag goipp.Tag, vals ...goipp.Value) {
		if len(vals) > 0 {
			a.Add(goipp.MakeAttr(name, tag, vals[0], vals[1:]...))
		}
	}
	add("printer-uri-supported", goipp.TagURI, goipp.String("ipp://"+host+"/ipp/print"))
	add("uri-authentication-supported", goipp.TagKeyword, goipp.String("none"))
	add("uri-security-supported", goipp.TagKeyword, goipp.String("none"))
	add("printer-name", goipp.TagName, goipp.String(p.cfg.Name))
	add("printer-make-and-model", goipp.TagText, goipp.String(p.cfg.Name))
	add("printer-state", goipp.TagEnum, goipp.Integer(p.cfg.State))
	add("printer-state-reasons", goipp.TagKeyword, goipp.String("none"))
	add("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(true))
	add("ipp-versions-supported", goipp.TagKeyword, goipp.String("1.1"), goipp.String("2.0"))
	add("charset-configured", goipp.TagCharset, goipp.String("utf-8"))
	add("charset-supported", goipp.TagCharset, goipp.String("utf-8"))
	add("natural-language-configured", goipp.TagLanguage, goipp.String("en-us"))
	add("generated-natural-language-supported", goipp.TagLanguage, goipp.String("en-us"))

	formats := make([]goipp.Value, 0, len(p.cfg.Formats))
	for _, f := range p.cfg.Formats {
		formats = append(formats, goipp.String(f.String()))
	}
	add("document-format-supported", goipp.TagMimeType, formats...)
	if len(p.cfg.Formats) > 0 {
		add("document-format-default", goipp.TagMimeType, goipp.String(p.cfg.Formats[0].String()))
	}
	addKeywords(a, "media-supported", p.cfg.Media)
	addKeywords(a, "sides-supported", p.cfg.Sides)
	addKeywords(a, "print-color-mode-supported", p.cfg.Colors)
	if res := resolutions(p.cfg.Resolutions); len(res) > 0 {
		a.Add(goipp.MakeAttr("printer-resolution-supported", goipp.TagResolution, res[0], res[1:]...))
	}
}

// --- helpers ---

func addKeywords(attrs *goipp.Attributes, name string, vals []string) {
	if len(vals) == 0 {
		return
	}
	gv := make([]goipp.Value, len(vals))
	for i, v := range vals {
		gv[i] = goipp.String(v)
	}
	attrs.Add(goipp.MakeAttr(name, goipp.TagKeyword, gv[0], gv[1:]...))
}

func resolutions(res []string) []goipp.Value {
	var out []goipp.Value
	for _, r := range res {
		s := strings.TrimSuffix(strings.TrimSuffix(r, "dpi"), "dpcm")
		x, y := s, s
		if i := strings.IndexByte(s, 'x'); i >= 0 {
			x, y = s[:i], s[i+1:]
		}
		xi, e1 := strconv.Atoi(x)
		yi, e2 := strconv.Atoi(y)
		if e1 == nil && e2 == nil {
			out = append(out, goipp.Resolution{Xres: xi, Yres: yi, Units: goipp.UnitsDpi})
		}
	}
	return out
}

func strFromGroup(attrs goipp.Attributes, name string) string {
	for _, at := range attrs {
		if at.Name == name && len(at.Values) > 0 {
			if s, ok := at.Values[0].V.(goipp.String); ok {
				return string(s)
			}
		}
	}
	return ""
}

func intFromGroup(attrs goipp.Attributes, name string) (int, bool) {
	for _, at := range attrs {
		if at.Name == name && len(at.Values) > 0 {
			if i, ok := at.Values[0].V.(goipp.Integer); ok {
				return int(i), true
			}
		}
	}
	return 0, false
}

func extFor(m model.MIME) string {
	switch m {
	case model.MIMEPDF:
		return ".pdf"
	case model.MIMEPWGRaster:
		return ".pwg"
	case model.MIMEURF:
		return ".urf"
	case model.MIMEJPEG:
		return ".jpg"
	case model.MIMEPNG:
		return ".png"
	case model.MIMEPostScript:
		return ".ps"
	case model.MIMEText:
		return ".txt"
	default:
		return ".bin"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Wait is a tiny helper for cmd usage: block until ctx is done.
func Wait(ctx context.Context) { <-ctx.Done() }
