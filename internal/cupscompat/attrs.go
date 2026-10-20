package cupscompat

import (
	"net/url"
	"os/user"
	"strconv"
	"time"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/model"
)

// CUPS printer-type bits (subset). Apps use this bitmask for icons/filtering.
const (
	cupsTypeColor  = 0x0004
	cupsTypeDuplex = 0x0008
	cupsTypeLocal  = 0x0000
)

// IPP job-state enum values.
const (
	ippJobPending    = 3
	ippJobHeld       = 4
	ippJobProcessing = 5
	ippJobCanceled   = 7
	ippJobAborted    = 8
	ippJobCompleted  = 9
)

// opAttrs adds the mandatory operation attributes to a response.
func opAttrs(m *goipp.Message) {
	m.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	m.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
}

// printerAttrs builds the attribute set for one printer, in the shape libcups
// (cupsGetDests) and the GTK/Qt print dialogs expect. The queue URI uses the
// CUPS local form ipp://localhost/printers/NAME.
func printerAttrs(p model.Printer, queued int) goipp.Attributes {
	// Preferred path: replay the real printer's full IPP attribute set, with the
	// queue-specific attributes overridden. This is what makes libcups clients
	// (Chromium's CUPS-IPP backend, Qt) accept the printer: they see the exact
	// rich attribute set the backend printer advertises (media-col-database,
	// ipp-features-supported, resolutions, ...), not a thin synthesized subset.
	if len(p.RawAttrs) > 0 {
		var msg goipp.Message
		if err := msg.DecodeBytes(p.RawAttrs); err == nil && len(msg.Printer) > 0 {
			return overrideQueueAttrs(msg.Printer, p, queued)
		}
	}

	var a goipp.Attributes
	queueURI := queueURI(p.Name)

	add := func(name string, tag goipp.Tag, vals ...goipp.Value) {
		if len(vals) > 0 {
			a.Add(goipp.MakeAttr(name, tag, vals[0], vals[1:]...))
		}
	}
	model0 := p.Caps.MakeAndModel
	if model0 == "" {
		model0 = p.Name
	}

	add("printer-name", goipp.TagName, goipp.String(p.Name))
	add("printer-uri-supported", goipp.TagURI, goipp.String(queueURI))
	add("uri-authentication-supported", goipp.TagKeyword, goipp.String("none"))
	add("uri-security-supported", goipp.TagKeyword, goipp.String("none"))
	add("printer-make-and-model", goipp.TagText, goipp.String(model0))
	add("device-uri", goipp.TagURI, goipp.String(p.URI))
	add("printer-type", goipp.TagEnum, goipp.Integer(printerType(p)))
	add("ipp-versions-supported", goipp.TagKeyword, goipp.String("1.1"), goipp.String("2.0"))
	add("charset-configured", goipp.TagCharset, goipp.String("utf-8"))
	add("charset-supported", goipp.TagCharset, goipp.String("utf-8"))
	add("natural-language-configured", goipp.TagLanguage, goipp.String("en-us"))
	add("generated-natural-language-supported", goipp.TagLanguage, goipp.String("en-us"))

	if formats := mimeValues(p.Caps.DocumentFormats); len(formats) > 0 {
		add("document-format-supported", goipp.TagMimeType, formats...)
		add("document-format-default", goipp.TagMimeType, formats[0])
	} else {
		add("document-format-supported", goipp.TagMimeType, goipp.String("application/pdf"))
		add("document-format-default", goipp.TagMimeType, goipp.String("application/pdf"))
	}
	addKeywords(&a, "media-supported", p.Caps.Media)
	addKeywords(&a, "media-default", firstOf(p.Caps.Media))
	addKeywords(&a, "sides-supported", p.Caps.Sides)
	addKeywords(&a, "sides-default", firstOf(p.Caps.Sides))
	addKeywords(&a, "print-color-mode-supported", p.Caps.ColorModes)
	for _, at := range queueAttrs(p, p.Name, queued) {
		a.Add(at)
	}
	return a
}

// queueAttrs describes the queue's own state and settings, which override
// whatever the device reports.
func queueAttrs(p model.Printer, info string, queued int) goipp.Attributes {
	var a goipp.Attributes
	if p.Queue.Info != "" {
		info = p.Queue.Info
	}
	state, reason, message := printerState(p), "none", ""
	if p.Queue.Paused {
		state, reason, message = 5, "paused", p.Queue.Message
		if message == "" {
			message = "Paused"
		}
	}
	a.Add(goipp.MakeAttribute("printer-info", goipp.TagText, goipp.String(info)))
	a.Add(goipp.MakeAttribute("printer-location", goipp.TagText, goipp.String(p.Queue.Location)))
	a.Add(goipp.MakeAttribute("printer-state", goipp.TagEnum, goipp.Integer(state)))
	a.Add(goipp.MakeAttribute("printer-state-reasons", goipp.TagKeyword, goipp.String(reason)))
	a.Add(goipp.MakeAttribute("printer-state-message", goipp.TagText, goipp.String(message)))
	a.Add(goipp.MakeAttribute("printer-state-change-time", goipp.TagInteger, goipp.Integer(p.Updated.Unix())))
	a.Add(goipp.MakeAttribute("printer-up-time", goipp.TagInteger, goipp.Integer(time.Now().Unix())))
	a.Add(goipp.MakeAttribute("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(!p.Queue.Rejecting)))
	a.Add(goipp.MakeAttribute("printer-is-shared", goipp.TagBoolean, goipp.Boolean(p.Queue.Shared)))
	a.Add(goipp.MakeAttribute("printer-is-temporary", goipp.TagBoolean, goipp.Boolean(false)))
	a.Add(goipp.MakeAttribute("queued-job-count", goipp.TagInteger, goipp.Integer(queued)))
	d := p.Queue.Defaults
	if d.Media != "" {
		a.Add(goipp.MakeAttribute("media-default", goipp.TagKeyword, goipp.String(d.Media)))
	}
	if d.Sides != "" {
		a.Add(goipp.MakeAttribute("sides-default", goipp.TagKeyword, goipp.String(d.Sides)))
	}
	if d.Color != "" {
		a.Add(goipp.MakeAttribute("print-color-mode-default", goipp.TagKeyword, goipp.String(d.Color)))
	}
	if d.Quality != 0 {
		a.Add(goipp.MakeAttribute("print-quality-default", goipp.TagEnum, goipp.Integer(d.Quality)))
	}
	if d.Copies != 0 {
		a.Add(goipp.MakeAttribute("copies-default", goipp.TagInteger, goipp.Integer(d.Copies)))
	}
	return a
}

func queueURI(name string) string {
	return "ipp://localhost/printers/" + url.PathEscape(name)
}

// overrideQueueAttrs takes the backend printer's real attributes and replaces
// the few that must describe the CUPS queue instead of the backend device, so
// clients submit jobs to Torchio (the queue), not directly to the printer.
func overrideQueueAttrs(real goipp.Attributes, p model.Printer, queued int) goipp.Attributes {
	queue := queueAttrs(p, displayName(real, p), queued)
	override := map[string]bool{
		"printer-name":                 true,
		"printer-uri-supported":        true,
		"uri-authentication-supported": true,
		"uri-security-supported":       true,
		"printer-info":                 true,
		"printer-more-info":            true,
		"printer-dns-sd-name":          true,
		"device-uri":                   true,
		"printer-type":                 true,
	}
	for _, at := range queue {
		override[at.Name] = true
	}
	var out goipp.Attributes
	for _, attr := range real {
		if !override[attr.Name] {
			out.Add(attr)
		}
	}
	out.Add(goipp.MakeAttribute("printer-name", goipp.TagName, goipp.String(p.Name)))
	out.Add(goipp.MakeAttribute("printer-uri-supported", goipp.TagURI, goipp.String(queueURI(p.Name))))
	out.Add(goipp.MakeAttribute("uri-authentication-supported", goipp.TagKeyword, goipp.String("none")))
	out.Add(goipp.MakeAttribute("uri-security-supported", goipp.TagKeyword, goipp.String("none")))
	out.Add(goipp.MakeAttribute("device-uri", goipp.TagURI, goipp.String(p.URI)))
	out.Add(goipp.MakeAttribute("printer-type", goipp.TagEnum, goipp.Integer(printerType(p))))
	for _, at := range queue {
		out.Add(at)
	}
	return out
}

// displayName is the human-facing label for the queue: the printer's real
// DNS-SD name (what CUPS shows), then its make-and-model, falling back to the
// queue name. This is why a queue the user called "brother" still appears as
// "Brother MFC-J5740DW" in the print dialog, matching system CUPS.
func displayName(real goipp.Attributes, p model.Printer) string {
	if s, ok := strAttr(real, "printer-dns-sd-name"); ok && s != "" {
		return s
	}
	if s, ok := strAttr(real, "printer-make-and-model"); ok && s != "" {
		return stripLocaleTag(s)
	}
	if p.Caps.MakeAndModel != "" {
		return stripLocaleTag(p.Caps.MakeAndModel)
	}
	return p.Name
}

// jobAttrs builds the attribute set for one job.
func jobAttrs(j Job) goipp.Attributes {
	var a goipp.Attributes
	add := func(name string, tag goipp.Tag, v goipp.Value) { a.Add(goipp.MakeAttribute(name, tag, v)) }
	add("job-id", goipp.TagInteger, goipp.Integer(j.Number))
	add("job-uri", goipp.TagURI, goipp.String("ipp://localhost/jobs/"+strconv.Itoa(j.Number)))
	add("job-printer-uri", goipp.TagURI, goipp.String(queueURI(j.Printer)))
	add("job-name", goipp.TagName, goipp.String(j.Name))
	add("job-originating-user-name", goipp.TagName, goipp.String(userName()))
	add("job-state", goipp.TagEnum, goipp.Integer(ippJobState(j.State)))
	reason := j.Reason
	if reason == "" {
		reason = jobReason(j.State)
	}
	add("job-state-reasons", goipp.TagKeyword, goipp.String(reason))
	add("job-k-octets", goipp.TagInteger, goipp.Integer((j.Size+1023)/1024))
	add("time-at-creation", goipp.TagInteger, goipp.Integer(j.Created.Unix()))
	if ippJobState(j.State) >= ippJobProcessing {
		add("time-at-processing", goipp.TagInteger, goipp.Integer(j.Updated.Unix()))
	}
	if j.State.IsTerminal() {
		add("time-at-completed", goipp.TagInteger, goipp.Integer(j.Updated.Unix()))
	}
	return a
}

func jobReason(s model.JobState) string {
	switch s {
	case model.JobHeld:
		return "job-hold-until-specified"
	case model.JobCompleted:
		return "job-completed-successfully"
	case model.JobCanceled:
		return "job-canceled-by-user"
	case model.JobAborted:
		return "aborted-by-system"
	case model.JobTransforming, model.JobSpooled, model.JobSending:
		return "job-printing"
	default:
		return "none"
	}
}

func userName() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "user"
}

func printerState(p model.Printer) int {
	if p.Caps.State != 0 {
		return p.Caps.State
	}
	return 3 // idle
}

func printerType(p model.Printer) int {
	t := cupsTypeLocal
	for _, c := range p.Caps.ColorModes {
		if c == "color" || c == "auto" {
			t |= cupsTypeColor
			break
		}
	}
	if p.Caps.SupportsDuplex() {
		t |= cupsTypeDuplex
	}
	return t
}

// ippJobState maps Torchio's job state to the IPP job-state enum.
func ippJobState(s model.JobState) int {
	switch s {
	case model.JobHeld:
		return ippJobHeld
	case model.JobTransforming, model.JobSpooled, model.JobSending:
		return ippJobProcessing
	case model.JobCompleted:
		return ippJobCompleted
	case model.JobCanceled:
		return ippJobCanceled
	case model.JobAborted:
		return ippJobAborted
	default:
		return ippJobPending
	}
}

// ticketFrom extracts a job ticket from the job-template attributes of a
// request (they live in the job group, sometimes echoed in operation).
func ticketFrom(req goipp.Message) model.JobTicket {
	t := model.JobTicket{}
	if n, ok := intAttr(req.Job, "copies"); ok {
		t.Copies = n
	}
	if v, ok := strAttr(req.Job, "sides"); ok {
		t.Sides = v
	}
	if v, ok := strAttr(req.Job, "media"); ok {
		t.Media = v
	}
	if v, ok := strAttr(req.Job, "print-color-mode"); ok {
		t.Color = v
	}
	if v, ok := intAttr(req.Job, "print-quality"); ok {
		t.Quality = v
	}
	if v, ok := strAttr(req.Job, "job-hold-until"); ok && v != "no-hold" {
		t.Hold = true
	}
	return t
}

func jobName(req goipp.Message) string {
	if v, ok := strAttr(req.Operation, "job-name"); ok {
		return v
	}
	return "document"
}

// --- attribute readers / small builders ---

func addKeywords(a *goipp.Attributes, name string, vals []string) {
	v := stringValues(vals)
	if len(v) == 0 {
		return
	}
	a.Add(goipp.MakeAttr(name, goipp.TagKeyword, v[0], v[1:]...))
}

func stringValues(ss []string) []goipp.Value {
	out := make([]goipp.Value, 0, len(ss))
	for _, s := range ss {
		out = append(out, goipp.String(s))
	}
	return out
}

func mimeValues(ms []model.MIME) []goipp.Value {
	out := make([]goipp.Value, 0, len(ms))
	for _, m := range ms {
		out = append(out, goipp.String(m.String()))
	}
	return out
}

func firstOf(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	return ss[:1]
}

func strAttr(attrs goipp.Attributes, name string) (string, bool) {
	for _, at := range attrs {
		if at.Name == name && len(at.Values) > 0 {
			if s, ok := at.Values[0].V.(goipp.String); ok {
				return string(s), true
			}
		}
	}
	return "", false
}

func intAttr(attrs goipp.Attributes, name string) (int, bool) {
	for _, at := range attrs {
		if at.Name == name && len(at.Values) > 0 {
			if i, ok := at.Values[0].V.(goipp.Integer); ok {
				return int(i), true
			}
		}
	}
	return 0, false
}

func boolAttr(attrs goipp.Attributes, name string) (bool, bool) {
	for _, at := range attrs {
		if at.Name == name && len(at.Values) > 0 {
			if b, ok := at.Values[0].V.(goipp.Boolean); ok {
				return bool(b), true
			}
		}
	}
	return false, false
}
