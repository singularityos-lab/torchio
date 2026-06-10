// Package host implements torchio-host: it hosts legacy printer drivers and
// exposes each as a local IPP printer (principle P1), in the spirit of PAPPL.
// A driver here is not a native binary: it is a WASM module plus a JSON
// capability manifest. The manifest is the PPD's replacement, typed and
// schema-validated instead of a 1992 PostScript dialect. The module receives a
// standard raster format and emits the printer's own language (PCL, ESC/P);
// the host translates the manifest into IPP attributes so the core sees an
// ordinary IPP printer.
package host

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/singularityos-lab/torchio/internal/model"
)

// Manifest describes a driver: what it consumes, what it produces, and the
// printer's capabilities. It is the diffable, validatable substitute for a PPD.
type Manifest struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Accepts      []string     `json:"accepts"`  // input MIME types (e.g. image/pwg-raster)
	Produces     string       `json:"produces"` // printer language (e.g. vendor/pcl5)
	Capabilities ManifestCaps `json:"capabilities"`
	Quirks       []string     `json:"quirks"`
}

// ManifestCaps is the capability block of a manifest.
type ManifestCaps struct {
	Media       []string `json:"media"`
	Resolutions []string `json:"resolutions"`
	Duplex      []string `json:"duplex"` // none | long-edge | short-edge
	Color       []string `json:"color"`  // monochrome | color
}

// ParseManifest decodes and validates a driver manifest.
func ParseManifest(r io.Reader) (*Manifest, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("host: parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks the required fields.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("host: manifest missing name")
	}
	if len(m.Accepts) == 0 {
		return fmt.Errorf("host: manifest %q accepts nothing", m.Name)
	}
	if m.Produces == "" {
		return fmt.Errorf("host: manifest %q produces nothing", m.Name)
	}
	return nil
}

// AcceptsFormat reports whether the driver consumes the given MIME.
func (m *Manifest) AcceptsFormat(mime model.MIME) bool {
	for _, a := range m.Accepts {
		if model.MIME(a) == mime {
			return true
		}
	}
	return false
}

// PrimaryInput is the format the host advertises as the printer's preferred
// document format (the first accepted type).
func (m *Manifest) PrimaryInput() model.MIME {
	return model.MIME(m.Accepts[0])
}

// IPPCapabilities translates the manifest into the typed capabilities the host
// advertises over IPP. The printer "accepts" exactly the driver's input
// formats; the core rasterizes (M3) to reach one of them.
func (m *Manifest) IPPCapabilities() model.Capabilities {
	caps := model.Capabilities{
		MakeAndModel: m.Name,
		State:        3, // idle
		Media:        m.Capabilities.Media,
		Resolutions:  m.Capabilities.Resolutions,
		ColorModes:   m.Capabilities.Color,
	}
	for _, a := range m.Accepts {
		caps.DocumentFormats = append(caps.DocumentFormats, model.MIME(a))
	}
	caps.Sides = duplexToSides(m.Capabilities.Duplex)
	return caps
}

// duplexToSides maps the manifest's duplex vocabulary to IPP "sides" values.
func duplexToSides(duplex []string) []string {
	if len(duplex) == 0 {
		return []string{"one-sided"}
	}
	var sides []string
	for _, d := range duplex {
		switch d {
		case "none":
			sides = append(sides, "one-sided")
		case "long-edge":
			sides = append(sides, "two-sided-long-edge")
		case "short-edge":
			sides = append(sides, "two-sided-short-edge")
		}
	}
	if len(sides) == 0 {
		sides = []string{"one-sided"}
	}
	return sides
}
