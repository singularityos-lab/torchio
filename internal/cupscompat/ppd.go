package cupscompat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/singularityos-lab/torchio/internal/model"
)

// Chromium's CUPS print backend (used by Chrome and OnlyOffice) fetches
// GET /printers/NAME.ppd and parses it for the page sizes, resolution, color
// and duplex it offers; if the fetch fails it drops the printer from the dialog.
// A driverless IPP printer has no vendor PPD, so - exactly like CUPS itself does
// for its temporary queues - we synthesize one from the printer's IPP
// capabilities. This is presentation only: jobs still come back to us over IPP
// and go through Torchio's transform graph.

// pageSize is one PPD media size in PostScript points.
type pageSize struct {
	name       string
	wpts, hpts int
}

// generatePPD builds a minimal but valid CUPS PPD from a printer's capabilities.
func generatePPD(p model.Printer) string {
	makeModel := stripLocaleTag(p.Caps.MakeAndModel)
	if makeModel == "" {
		makeModel = p.Name
	}
	color := hasColorMode(p)

	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...); b.WriteByte('\n') }

	w(`*PPD-Adobe: "4.3"`)
	w(`*FormatVersion: "4.3"`)
	w(`*FileVersion: "1.0"`)
	w(`*LanguageVersion: English`)
	w(`*LanguageEncoding: ISOLatin1`)
	w(`*PCFileName: "TORCHIO.PPD"`)
	w(`*Manufacturer: "%s"`, ppdQuote(firstWord(makeModel)))
	w(`*Product: "(%s)"`, ppdQuote(makeModel))
	w(`*ModelName: "%s"`, ppdQuote(makeModel))
	w(`*ShortNickName: "%s"`, ppdQuote(truncate(makeModel, 31)))
	w(`*NickName: "%s"`, ppdQuote(makeModel))
	w(`*PSVersion: "(3010.000) 0"`)
	w(`*LanguageLevel: "3"`)
	w(`*FileSystem: False`)
	w(`*Throughput: "1"`)
	w(`*LandscapeOrientation: Plus90`)
	w(`*TTRasterizer: Type42`)
	w(`*cupsVersion: 2.4`)
	w(`*cupsModelNumber: 0`)
	w(`*cupsManualCopies: False`)
	// The queue accepts PDF; Torchio's transform graph takes it from there. This
	// also makes Chromium emit PDF rather than PostScript.
	w(`*cupsFilter2: "application/vnd.cups-pdf application/pdf 0 -"`)

	if color {
		w(`*ColorDevice: True`)
		w(`*DefaultColorSpace: RGB`)
	} else {
		w(`*ColorDevice: False`)
		w(`*DefaultColorSpace: Gray`)
	}

	sizes := pageSizes(p)
	if len(sizes) == 0 {
		sizes = []pageSize{{"A4", 595, 842}, {"Letter", 612, 792}}
	}
	def := sizes[0].name

	w(`*OpenUI *PageSize/Media Size: PickOne`)
	w(`*OrderDependency: 10 AnySetup *PageSize`)
	w(`*DefaultPageSize: %s`, def)
	for _, s := range sizes {
		w(`*PageSize %s/%s: "<</PageSize[%d %d]>>setpagedevice"`, s.name, s.name, s.wpts, s.hpts)
	}
	w(`*CloseUI: *PageSize`)

	w(`*OpenUI *PageRegion/Media Size: PickOne`)
	w(`*OrderDependency: 10 AnySetup *PageRegion`)
	w(`*DefaultPageRegion: %s`, def)
	for _, s := range sizes {
		w(`*PageRegion %s/%s: "<</PageSize[%d %d]>>setpagedevice"`, s.name, s.name, s.wpts, s.hpts)
	}
	w(`*CloseUI: *PageRegion`)

	w(`*DefaultImageableArea: %s`, def)
	for _, s := range sizes {
		w(`*ImageableArea %s/%s: "0 0 %d %d"`, s.name, s.name, s.wpts, s.hpts)
	}
	w(`*DefaultPaperDimension: %s`, def)
	for _, s := range sizes {
		w(`*PaperDimension %s/%s: "%d %d"`, s.name, s.name, s.wpts, s.hpts)
	}

	res := resolutionsOf(p)
	if len(res) == 0 {
		res = []string{"300dpi"}
	}
	w(`*OpenUI *Resolution/Resolution: PickOne`)
	w(`*OrderDependency: 20 AnySetup *Resolution`)
	w(`*DefaultResolution: %s`, res[0])
	for _, r := range res {
		dpi := strings.TrimSuffix(r, "dpi")
		w(`*Resolution %s/%s: "<</HWResolution[%s %s]>>setpagedevice"`, r, r, dpi, dpi)
	}
	w(`*CloseUI: *Resolution`)

	w(`*OpenUI *ColorModel/Color Mode: PickOne`)
	w(`*OrderDependency: 10 AnySetup *ColorModel`)
	if color {
		w(`*DefaultColorModel: RGB`)
		w(`*ColorModel RGB/Color: "<</cupsColorSpace 1/cupsColorOrder 0/cupsBitsPerColor 8>>setpagedevice"`)
		w(`*ColorModel Gray/Grayscale: "<</cupsColorSpace 0/cupsColorOrder 0/cupsBitsPerColor 8>>setpagedevice"`)
	} else {
		w(`*DefaultColorModel: Gray`)
		w(`*ColorModel Gray/Grayscale: "<</cupsColorSpace 0/cupsColorOrder 0/cupsBitsPerColor 8>>setpagedevice"`)
	}
	w(`*CloseUI: *ColorModel`)

	if p.Caps.SupportsDuplex() {
		w(`*OpenUI *Duplex/2-Sided Printing: PickOne`)
		w(`*OrderDependency: 20 AnySetup *Duplex`)
		w(`*DefaultDuplex: None`)
		w(`*Duplex None/Off: "<</Duplex false>>setpagedevice"`)
		w(`*Duplex DuplexNoTumble/On (Long Edge): "<</Duplex true/Tumble false>>setpagedevice"`)
		w(`*Duplex DuplexTumble/On (Short Edge): "<</Duplex true/Tumble true>>setpagedevice"`)
		w(`*CloseUI: *Duplex`)
	}

	return b.String()
}

// pageSizes derives the PPD media list from the printer's IPP media keywords,
// whose PWG names already encode the dimensions (e.g. iso_a4_210x297mm).
func pageSizes(p model.Printer) []pageSize {
	var out []pageSize
	seen := map[string]bool{}
	for _, m := range p.Caps.Media {
		ps, ok := parsePWGMedia(m)
		if !ok || seen[ps.name] {
			continue
		}
		seen[ps.name] = true
		out = append(out, ps)
	}
	return out
}

// parsePWGMedia turns a self-describing PWG media name into a PPD page size.
func parsePWGMedia(media string) (pageSize, bool) {
	parts := strings.Split(media, "_")
	if len(parts) < 3 {
		return pageSize{}, false
	}
	dim := parts[len(parts)-1]
	token := strings.Join(parts[1:len(parts)-1], "_")

	var unit string
	switch {
	case strings.HasSuffix(dim, "mm"):
		unit, dim = "mm", strings.TrimSuffix(dim, "mm")
	case strings.HasSuffix(dim, "in"):
		unit, dim = "in", strings.TrimSuffix(dim, "in")
	default:
		return pageSize{}, false
	}
	wh := strings.SplitN(dim, "x", 2)
	if len(wh) != 2 {
		return pageSize{}, false
	}
	wf, err1 := strconv.ParseFloat(wh[0], 64)
	hf, err2 := strconv.ParseFloat(wh[1], 64)
	if err1 != nil || err2 != nil {
		return pageSize{}, false
	}
	toPts := func(v float64) int {
		if unit == "mm" {
			return int(v/25.4*72.0 + 0.5)
		}
		return int(v*72.0 + 0.5)
	}
	return pageSize{name: ppdSizeName(token), wpts: toPts(wf), hpts: toPts(hf)}, true
}

// ppdSizeName maps a PWG size token to a canonical PPD option name, falling back
// to a sanitized form for sizes we do not special-case.
func ppdSizeName(token string) string {
	switch token {
	case "a3":
		return "A3"
	case "a4":
		return "A4"
	case "a5":
		return "A5"
	case "a6":
		return "A6"
	case "b5":
		return "B5"
	case "letter":
		return "Letter"
	case "legal":
		return "Legal"
	case "executive":
		return "Executive"
	case "tabloid", "ledger":
		return "Tabloid"
	case "photo":
		return "Photo"
	}
	return sanitizePPDName(token)
}

// sanitizePPDName makes an arbitrary token a legal PPD option keyword: letters
// and digits only, beginning with a letter.
func sanitizePPDName(token string) string {
	var b strings.Builder
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		return "Custom"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "Size" + s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func resolutionsOf(p model.Printer) []string {
	return p.Caps.Resolutions
}

func hasColorMode(p model.Printer) bool {
	for _, c := range p.Caps.ColorModes {
		if c == "color" || c == "auto" {
			return true
		}
	}
	return false
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ppdQuote strips characters that would break a PPD quoted string.
func ppdQuote(s string) string {
	return strings.ReplaceAll(s, `"`, "")
}

// stripLocaleTag removes a trailing " [xx]" language tag that IPP printers often
// append to printer-make-and-model / printer-info (e.g. "Brother MFC [en]").
func stripLocaleTag(s string) string {
	if strings.HasSuffix(s, "]") {
		if i := strings.LastIndex(s, " ["); i > 0 {
			return s[:i]
		}
	}
	return s
}

// ppdQueueName extracts the queue name from a /printers/NAME.ppd request path.
func ppdQueueName(path string) (string, bool) {
	if !strings.HasSuffix(path, ".ppd") {
		return "", false
	}
	base := strings.TrimSuffix(path, ".ppd")
	i := strings.LastIndex(base, "/")
	if i < 0 {
		return "", false
	}
	return base[i+1:], true
}
