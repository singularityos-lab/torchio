package detect

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/singularityos-lab/torchio/internal/model"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want model.MIME
	}{
		{"pdf", []byte("%PDF-1.7\n..."), model.MIMEPDF},
		{"pdf with leading junk", append([]byte("\xef\xbb\xbf\n   "), []byte("%PDF-1.4")...), model.MIMEPDF},
		{"postscript", []byte("%!PS-Adobe-3.0\n"), model.MIMEPostScript},
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}, model.MIMEJPEG},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00"), model.MIMEPNG},
		{"pwg raster", []byte("RaS2............"), model.MIMEPWGRaster},
		{"urf", []byte("UNIRAST\x00........"), model.MIMEURF},
		{"plain text", []byte("Hello, this is a plain text document.\n"), model.MIMEText},
		{"binary garbage", []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00, 0x13}, model.MIMEOctet},
		{"empty", []byte{}, model.MIMEOctet},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatBytes(c.data); got != c.want {
				t.Errorf("FormatBytes(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestFormatReplaysStream verifies the streaming detector does not swallow any
// bytes: the returned reader must reproduce the entire original input.
func TestFormatReplaysStream(t *testing.T) {
	original := "%PDF-1.7\n" + strings.Repeat("payload-", 500) + "\n%%EOF\n"
	mime, r, err := Format(strings.NewReader(original))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if mime != model.MIMEPDF {
		t.Fatalf("mime = %v, want PDF", mime)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, []byte(original)) {
		t.Errorf("replayed stream differs: got %d bytes, want %d", len(got), len(original))
	}
}
