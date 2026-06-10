package usb

import (
	"strings"
	"testing"
)

const sampleQuirks = `
# Torchio USB quirks
[*:*]
usb-max-interfaces = 2     ; sane default

[03f0:*]                    # all HP printers
zlp-send = true

[04b8:0202]                 ; specific Epson model
usb-max-interfaces = 1
reset-before = yes
`

func TestQuirksLookupMerge(t *testing.T) {
	db, err := ParseQuirks(strings.NewReader(sampleQuirks))
	if err != nil {
		t.Fatalf("ParseQuirks: %v", err)
	}

	// An unknown vendor gets only the global default.
	q := db.Lookup(0x1234, 0x5678)
	if q.MaxInterfaces != 2 || q.ZlpSend || q.ResetBefore {
		t.Errorf("default lookup = %+v", q)
	}

	// An HP printer gets the default plus the HP wildcard (zlp-send).
	q = db.Lookup(0x03f0, 0x9999)
	if q.MaxInterfaces != 2 || !q.ZlpSend {
		t.Errorf("HP lookup = %+v", q)
	}

	// The specific Epson overrides the global max-interfaces and adds reset.
	q = db.Lookup(0x04b8, 0x0202)
	if q.MaxInterfaces != 1 {
		t.Errorf("specific should override max-interfaces, got %d", q.MaxInterfaces)
	}
	if !q.ResetBefore {
		t.Errorf("expected reset-before for the Epson")
	}
	if q.Raw["reset-before"] != "yes" {
		t.Errorf("raw map missing reset-before: %+v", q.Raw)
	}
}

func TestQuirksParseErrors(t *testing.T) {
	for _, bad := range []string{
		"key = value\n",       // key outside a section
		"[nothex:0202]\n",     // bad vendor
		"[03f0:0202]\nnope\n", // line without '='
	} {
		if _, err := ParseQuirks(strings.NewReader(bad)); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestQuirksEmptyDB(t *testing.T) {
	db, err := ParseQuirks(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	q := db.Lookup(0x03f0, 0x1234)
	if q.MaxInterfaces != 0 || q.ZlpSend {
		t.Errorf("empty db should yield zero quirks, got %+v", q)
	}
}
