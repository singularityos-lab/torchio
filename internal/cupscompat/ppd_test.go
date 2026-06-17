package cupscompat

import (
	"strings"
	"testing"

	"github.com/singularityos-lab/torchio/internal/model"
)

func TestParsePWGMedia(t *testing.T) {
	cases := []struct {
		in         string
		name       string
		wpts, hpts int
		ok         bool
	}{
		{"iso_a4_210x297mm", "A4", 595, 842, true},
		{"na_letter_8.5x11in", "Letter", 612, 792, true},
		{"na_legal_8.5x14in", "Legal", 612, 1008, true},
		{"garbage", "", 0, 0, false},
	}
	for _, c := range cases {
		ps, ok := parsePWGMedia(c.in)
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if ps.name != c.name || ps.wpts != c.wpts || ps.hpts != c.hpts {
			t.Errorf("%s: got %s %dx%d, want %s %dx%d", c.in, ps.name, ps.wpts, ps.hpts, c.name, c.wpts, c.hpts)
		}
	}
}

func TestStripLocaleTag(t *testing.T) {
	for in, want := range map[string]string{
		"Brother MFC-J5740DW [en]": "Brother MFC-J5740DW",
		"Brother MFC-J5740DW":      "Brother MFC-J5740DW",
		"Foo [bar] baz":            "Foo [bar] baz", // tag must be trailing
	} {
		if got := stripLocaleTag(in); got != want {
			t.Errorf("stripLocaleTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneratePPD(t *testing.T) {
	p := model.Printer{
		Name: "Brother_MFC-J5740DW",
		Caps: model.Capabilities{
			MakeAndModel: "Brother MFC-J5740DW [en]",
			Media:        []string{"iso_a4_210x297mm", "na_letter_8.5x11in"},
			ColorModes:   []string{"color", "monochrome"},
			Resolutions:  []string{"600dpi"},
			Sides:        []string{"one-sided", "two-sided-long-edge"},
		},
	}
	ppd := generatePPD(p)
	must := []string{
		`*PPD-Adobe: "4.3"`,
		`*NickName: "Brother MFC-J5740DW"`, // locale tag stripped
		`*DefaultPageSize: A4`,
		`*PageSize A4/A4:`,
		`*PageSize Letter/Letter:`,
		`*DefaultResolution: 600dpi`,
		`*ColorModel RGB/Color:`,
		`*OpenUI *Duplex`,
	}
	for _, m := range must {
		if !strings.Contains(ppd, m) {
			t.Errorf("generated PPD missing %q\n---\n%s", m, ppd)
		}
	}
}
