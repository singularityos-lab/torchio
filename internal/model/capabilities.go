package model

// Capabilities is the typed snapshot of what a printer can do, derived from its
// IPP attributes. It is explicitly NOT a PPD: there is no PostScript, no vendor
// dialect, just the standard IPP attributes parsed into Go values. The raw IPP
// message is kept alongside this (by the caller) for total fidelity.
type Capabilities struct {
	// MakeAndModel is "printer-make-and-model", for display.
	MakeAndModel string
	// State is the IPP "printer-state" enum (3=idle, 4=processing, 5=stopped).
	State int
	// StateReasons is "printer-state-reasons" (e.g. "media-empty").
	StateReasons []string

	// DocumentFormats is "document-format-supported": the formats the printer
	// accepts directly. The transform planner targets this set.
	DocumentFormats []MIME
	// Media is "media-supported" (e.g. iso_a4_210x297mm).
	Media []string
	// Sides is "sides-supported" (one-sided, two-sided-long-edge, ...).
	Sides []string
	// Resolutions is "printer-resolution-supported", as "<x>x<y>dpi" strings.
	Resolutions []string
	// ColorModes is "print-color-mode-supported" (monochrome, color, auto).
	ColorModes []string
}

// AcceptsFormat reports whether the printer takes the given MIME directly,
// meaning no transform is needed (the fast path).
func (c Capabilities) AcceptsFormat(m MIME) bool {
	for _, f := range c.DocumentFormats {
		if f == m {
			return true
		}
	}
	return false
}

// SupportsDuplex reports whether any two-sided mode is advertised.
func (c Capabilities) SupportsDuplex() bool {
	for _, s := range c.Sides {
		if s != "one-sided" && s != "" {
			return true
		}
	}
	return false
}
