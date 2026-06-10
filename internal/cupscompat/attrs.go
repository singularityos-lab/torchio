package cupscompat

import (
	"strconv"

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
func printerAttrs(p model.Printer) goipp.Attributes {
	var a goipp.Attributes
	queueURI := "ipp://localhost/printers/" + p.Name

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
	add("printer-info", goipp.TagText, goipp.String(p.Name))
	add("printer-location", goipp.TagText, goipp.String(""))
	add("printer-make-and-model", goipp.TagText, goipp.String(model0))
	add("device-uri", goipp.TagURI, goipp.String(p.URI))
	add("printer-state", goipp.TagEnum, goipp.Integer(printerState(p)))
	add("printer-state-reasons", goipp.TagKeyword, goipp.String("none"))
	add("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(true))
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
	return a
}

// jobAttrs builds the attribute set for one job.
func jobAttrs(j *compatJob, state model.JobState) goipp.Attributes {
	var a goipp.Attributes
	a.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(j.id)))
	a.Add(goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String("ipp://localhost/jobs/"+strconv.Itoa(j.id))))
	a.Add(goipp.MakeAttribute("job-printer-uri", goipp.TagURI, goipp.String("ipp://localhost/printers/"+j.printer)))
	if j.name != "" {
		a.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String(j.name)))
	}
	a.Add(goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(ippJobState(state))))
	a.Add(goipp.MakeAttribute("job-state-reasons", goipp.TagKeyword, goipp.String("none")))
	return a
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
