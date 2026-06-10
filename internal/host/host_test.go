package host

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/transform"
)

const sampleManifest = `{
  "name": "generic-pcl5",
  "version": "1.2.0",
  "accepts": ["image/pwg-raster"],
  "produces": "vendor/pcl5",
  "capabilities": {
    "media": ["iso_a4_210x297mm", "na_letter_8.5x11in"],
    "resolutions": ["300dpi", "600dpi"],
    "duplex": ["none"],
    "color": ["monochrome"]
  },
  "quirks": ["needs-trailing-formfeed"]
}`

func TestManifestParse(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(sampleManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Name != "generic-pcl5" || m.Produces != "vendor/pcl5" {
		t.Errorf("manifest = %+v", m)
	}
	if !m.AcceptsFormat(model.MIMEPWGRaster) {
		t.Errorf("should accept pwg-raster")
	}
	caps := m.IPPCapabilities()
	if len(caps.DocumentFormats) != 1 || caps.DocumentFormats[0] != model.MIMEPWGRaster {
		t.Errorf("caps formats = %v", caps.DocumentFormats)
	}
	if len(caps.Sides) != 1 || caps.Sides[0] != "one-sided" {
		t.Errorf("duplex none should map to one-sided, got %v", caps.Sides)
	}
}

func TestManifestValidation(t *testing.T) {
	for _, bad := range []string{
		`{"accepts":["x"],"produces":"y"}`,        // no name
		`{"name":"n","produces":"y"}`,             // no accepts
		`{"name":"n","accepts":["x"]}`,            // no produces
		`{"name":"n","accepts":["x"],"prod":"y"}`, // unknown field
	} {
		if _, err := ParseManifest(strings.NewReader(bad)); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

// goDriver is an in-Go transform standing in for a WASM driver, to test the
// host wiring without the wasm runtime.
type goDriver struct {
	from, to model.MIME
	fn       func([]byte) []byte
}

func (d goDriver) Edge() transform.Edge {
	return transform.Edge{From: d.from, To: d.to, Cost: 10}
}
func (d goDriver) Run(_ context.Context, in io.Reader, out io.Writer, _ transform.Params) error {
	b, _ := io.ReadAll(in)
	_, err := out.Write(d.fn(b))
	return err
}

// serveHost runs a host's IPP server on an httptest server and returns its URL.
func serveHost(t *testing.T, h *Host) string {
	t.Helper()
	srv := httptest.NewServer(h.server)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestHostIPPServerAndPipeline(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	backend := &BufferBackend{}
	// Driver: uppercases its input (stand-in for pwg-raster -> pcl5).
	driver := goDriver{from: model.MIMEPWGRaster, to: "vendor/pcl5", fn: bytes.ToUpper}
	h := New(m, driver, backend)
	url := serveHost(t, h)

	client := ipp.NewClient()

	// The core can probe the hosted printer like any IPP printer.
	msg, err := client.GetPrinterAttributes(context.Background(), url)
	if err != nil {
		t.Fatalf("GetPrinterAttributes: %v", err)
	}
	caps := ipp.ParseCapabilities(msg)
	if caps.MakeAndModel != "generic-pcl5" {
		t.Errorf("make/model = %q", caps.MakeAndModel)
	}
	if !caps.AcceptsFormat(model.MIMEPWGRaster) {
		t.Errorf("hosted printer should advertise pwg-raster, got %v", caps.DocumentFormats)
	}
	if len(caps.Resolutions) != 2 {
		t.Errorf("resolutions = %v, want 2", caps.Resolutions)
	}

	// Printing sends a document; the driver runs and the backend receives the
	// printer-language output.
	doc := []byte("rasterdata-here")
	res, err := client.PrintJob(context.Background(), ipp.PrintRequest{
		PrinterURI:     url,
		JobName:        "test",
		DocumentFormat: "image/pwg-raster",
		Document:       bytes.NewReader(doc),
	})
	if err != nil {
		t.Fatalf("PrintJob: %v", err)
	}
	if res.JobID == 0 {
		t.Errorf("expected a job id")
	}
	if got := backend.Bytes(); string(got) != "RASTERDATA-HERE" {
		t.Errorf("backend received %q, want uppercased", got)
	}
}
