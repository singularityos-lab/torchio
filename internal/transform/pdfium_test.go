package transform

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/singularityos-lab/torchio/internal/model"
)

// TestPDFiumRasterizes is the full rasterization path: an image is wrapped into
// a PDF (the native transform), then pdfium-in-wazero renders that PDF back to
// pixels, which are encoded as PWG Raster and decoded for inspection. It proves
// pdfium actually runs in the sandbox and produces real output.
func TestPDFiumRasterizes(t *testing.T) {
	// Build a recognizable image: left half red, right half blue.
	const w, h = 32, 24
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.SetRGBA(x, y, color.RGBA{R: 220, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{B: 220, A: 255})
			}
		}
	}

	// image -> PDF (the native transform's writer, fed the image directly)
	var pdf bytes.Buffer
	if err := writeImagePDF(&pdf, img); err != nil {
		t.Fatalf("writeImagePDF: %v", err)
	}

	// PDF -> PWG raster, via pdfium.
	pf := NewPDFiumTransform(150)
	defer pf.Close()
	if pf.Edge().From != model.MIMEPDF || pf.Edge().To != model.MIMEPWGRaster {
		t.Fatalf("edge = %+v", pf.Edge())
	}

	var pwg bytes.Buffer
	if err := pf.Run(context.Background(), &pdf, &pwg, Params{}); err != nil {
		t.Fatalf("pdfium Run: %v", err)
	}

	// Decode the PWG raster and sanity-check it.
	pages, err := decodePWG(pwg.Bytes())
	if err != nil {
		t.Fatalf("decodePWG: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	p := pages[0]
	// 32pt x 24pt page at 150 DPI -> ~67 x 50 px.
	if p.Width < 40 || p.Height < 30 {
		t.Errorf("rasterized size unexpectedly small: %dx%d", p.Width, p.Height)
	}

	// Some pixel near the left should be reddish, near the right bluish: this
	// confirms pdfium drew real content, not a blank page.
	red := pixelAt(p, p.Width/4, p.Height/2)
	blue := pixelAt(p, 3*p.Width/4, p.Height/2)
	if red[0] <= red[2] {
		t.Errorf("left side not reddish: %v", red)
	}
	if blue[2] <= blue[0] {
		t.Errorf("right side not bluish: %v", blue)
	}
}

func pixelAt(p pwgPage, x, y int) [3]byte {
	i := (y*p.Width + x) * 3
	if i+2 >= len(p.RGB) {
		return [3]byte{}
	}
	return [3]byte{p.RGB[i], p.RGB[i+1], p.RGB[i+2]}
}
