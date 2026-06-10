package usb

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Quirks captures the known firmware misbehaviors of a printer model. Twenty
// years of broken firmware cannot be designed away, so quirks live as data
// (a text file), never as code branches. This mirrors the ipp-usb quirks idea.
type Quirks struct {
	// MaxInterfaces caps how many IPP-over-USB interfaces to use at once. Some
	// printers misbehave when driven over more than one. 0 means no cap.
	MaxInterfaces int
	// ZlpSend sends a zero-length packet after a bulk-OUT whose size is a
	// multiple of the endpoint packet size, which some printers require to mark
	// the end of a transfer.
	ZlpSend bool
	// ResetBefore issues a USB reset before first use (for printers that wedge
	// after a previous session).
	ResetBefore bool
	// Raw holds every key=value seen, including ones not modeled above.
	Raw map[string]string
}

// section is one matched block of the quirks file.
type section struct {
	vendor  uint16
	product uint16
	anyVend bool
	anyProd bool
	keys    map[string]string
}

// QuirksDB is the parsed set of quirk sections.
type QuirksDB struct {
	sections []section
}

// ParseQuirks reads a quirks file. The format is INI-like:
//
//	# comment
//	[03f0:*]            ; vendor 0x03f0, any product
//	usb-max-interfaces = 1
//	zlp-send = true
//
//	[04b8:0202]
//	reset-before = true
//
// A section header is VVVV:PPPP in hex, where either field may be '*' to match
// any. Later, Lookup merges all matching sections, most specific last.
func ParseQuirks(r io.Reader) (*QuirksDB, error) {
	db := &QuirksDB{}
	var cur *section
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(stripComment(sc.Text()))
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			s, err := parseHeader(text[1 : len(text)-1])
			if err != nil {
				return nil, fmt.Errorf("quirks: line %d: %w", line, err)
			}
			db.sections = append(db.sections, s)
			cur = &db.sections[len(db.sections)-1]
			continue
		}
		k, v, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("quirks: line %d: expected key = value", line)
		}
		if cur == nil {
			return nil, fmt.Errorf("quirks: line %d: key outside any section", line)
		}
		cur.keys[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return db, sc.Err()
}

// Lookup returns the merged quirks for a device, applying every matching
// section in order (a specific VID:PID overrides a wildcard).
func (db *QuirksDB) Lookup(vendor, product uint16) Quirks {
	q := Quirks{Raw: map[string]string{}}
	// Apply wildcard sections first, specific ones last, so specific wins.
	for _, specificFirst := range []bool{false, true} {
		for _, s := range db.sections {
			if !s.matches(vendor, product) {
				continue
			}
			isSpecific := !s.anyVend && !s.anyProd
			if isSpecific != specificFirst {
				continue
			}
			for k, v := range s.keys {
				q.Raw[k] = v
				applyKnown(&q, k, v)
			}
		}
	}
	return q
}

func (s section) matches(vendor, product uint16) bool {
	if !s.anyVend && s.vendor != vendor {
		return false
	}
	if !s.anyProd && s.product != product {
		return false
	}
	return true
}

func applyKnown(q *Quirks, key, val string) {
	switch key {
	case "usb-max-interfaces":
		if n, err := strconv.Atoi(val); err == nil {
			q.MaxInterfaces = n
		}
	case "zlp-send":
		q.ZlpSend = truthy(val)
	case "reset-before":
		q.ResetBefore = truthy(val)
	}
}

func parseHeader(h string) (section, error) {
	vend, prod, ok := strings.Cut(h, ":")
	if !ok {
		return section{}, fmt.Errorf("bad section %q (want VVVV:PPPP)", h)
	}
	s := section{keys: map[string]string{}}
	if vend == "*" {
		s.anyVend = true
	} else {
		v, err := strconv.ParseUint(vend, 16, 16)
		if err != nil {
			return section{}, fmt.Errorf("bad vendor %q", vend)
		}
		s.vendor = uint16(v)
	}
	if prod == "*" {
		s.anyProd = true
	} else {
		p, err := strconv.ParseUint(prod, 16, 16)
		if err != nil {
			return section{}, fmt.Errorf("bad product %q", prod)
		}
		s.product = uint16(p)
	}
	return s, nil
}

// stripComment removes trailing '#' or ';' comments.
func stripComment(s string) string {
	for i, c := range s {
		if c == '#' || c == ';' {
			return s[:i]
		}
	}
	return s
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "1", "on":
		return true
	default:
		return false
	}
}
