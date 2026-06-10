package ipp

import "testing"

func TestNormalizeURI(t *testing.T) {
	cases := []struct {
		in            string
		wantCanonical string
		wantTransport string
		wantErr       bool
	}{
		{"ipp://printer.local/ipp/print", "ipp://printer.local:631/ipp/print", "http://printer.local:631/ipp/print", false},
		{"ipps://printer.local/ipp/print", "ipps://printer.local:631/ipp/print", "https://printer.local:631/ipp/print", false},
		{"ipp://printer.local", "ipp://printer.local:631/ipp/print", "http://printer.local:631/ipp/print", false},
		{"ipp://printer.local:9100/queue", "ipp://printer.local:9100/queue", "http://printer.local:9100/queue", false},
		{"http://10.0.0.5:631/ipp/print", "ipp://10.0.0.5:631/ipp/print", "http://10.0.0.5:631/ipp/print", false},
		{"https://10.0.0.5/ipp/print", "ipps://10.0.0.5:631/ipp/print", "https://10.0.0.5:631/ipp/print", false},
		{"", "", "", true},
		{"printer.local", "", "", true},       // no scheme
		{"ftp://printer.local", "", "", true}, // unsupported scheme
		{"ipp://", "", "", true},              // no host
	}
	for _, c := range cases {
		canon, trans, err := normalizeURI(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeURI(%q): expected error, got %q/%q", c.in, canon, trans)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeURI(%q): unexpected error %v", c.in, err)
			continue
		}
		if canon != c.wantCanonical {
			t.Errorf("normalizeURI(%q) canonical = %q, want %q", c.in, canon, c.wantCanonical)
		}
		if trans != c.wantTransport {
			t.Errorf("normalizeURI(%q) transport = %q, want %q", c.in, trans, c.wantTransport)
		}
	}
}
