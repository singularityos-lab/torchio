package ipp

import (
	"fmt"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/model"
)

// ParseCapabilities turns a Get-Printer-Attributes response into the typed
// model.Capabilities. It reads only the printer group, where Get-Printer-
// Attributes places the answer.
func ParseCapabilities(msg *goipp.Message) model.Capabilities {
	p := msg.Printer

	caps := model.Capabilities{
		Media:        Strings(p, "media-supported"),
		Sides:        Strings(p, "sides-supported"),
		ColorModes:   Strings(p, "print-color-mode-supported"),
		StateReasons: Strings(p, "printer-state-reasons"),
	}

	if s, ok := FirstString(p, "printer-make-and-model"); ok {
		caps.MakeAndModel = s
	}
	if st, ok := FirstInt(p, "printer-state"); ok {
		caps.State = st
	}

	for _, f := range Strings(p, "document-format-supported") {
		caps.DocumentFormats = append(caps.DocumentFormats, model.MIME(f))
	}

	for _, r := range Resolutions(p, "printer-resolution-supported") {
		caps.Resolutions = append(caps.Resolutions, formatResolution(r))
	}

	return caps
}

// formatResolution renders an IPP resolution as "600dpi" (square) or
// "600x300dpi" (non-square). Non-dpi units fall back to a generic suffix.
func formatResolution(r goipp.Resolution) string {
	unit := "dpi"
	if r.Units != goipp.UnitsDpi {
		unit = "dpcm"
	}
	if r.Xres == r.Yres {
		return fmt.Sprintf("%d%s", r.Xres, unit)
	}
	return fmt.Sprintf("%dx%d%s", r.Xres, r.Yres, unit)
}
