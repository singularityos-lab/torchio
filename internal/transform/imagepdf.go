package transform

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder
	_ "image/png"  // register the PNG decoder
	"io"

	"github.com/singularityos-lab/torchio/internal/model"
)

// imageToPDF wraps a raster image into a single-page PDF. It is a pure-Go
// transform (no pdfium needed) that lets an image print on a PDF-only printer.
// The image is embedded as a FlateDecode DeviceRGB XObject sized to one page at
// 72 dpi.
type imageToPDF struct {
	from model.MIME
}

// Edge places the transform in the graph: image -> PDF at a moderate cost
// (higher than passthrough, since it loses the option of vector output).
func (t imageToPDF) Edge() Edge {
	return Edge{From: t.from, To: model.MIMEPDF, Cost: 10}
}

// Run decodes the image and writes a one-page PDF embedding it.
func (t imageToPDF) Run(_ context.Context, in io.Reader, out io.Writer, _ Params) error {
	img, _, err := image.Decode(in)
	if err != nil {
		return fmt.Errorf("transform: decode image: %w", err)
	}
	return writeImagePDF(out, img)
}

// DefaultRegistry returns the registry of built-in native transforms. pdfium
// (PDF -> PWG raster) and other WASM modules are registered on top of this when
// available; this base set is pure Go and always present.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(imageToPDF{from: model.MIMEJPEG})
	r.Register(imageToPDF{from: model.MIMEPNG})
	return r
}

// writeImagePDF emits a minimal, valid PDF with the image as its only page.
func writeImagePDF(w io.Writer, img image.Image) error {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("transform: empty image")
	}

	// Pack pixels as top-to-bottom, left-to-right RGB (PDF image order).
	rgb := make([]byte, 0, width*height*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA() // 16-bit per channel
			rgb = append(rgb, byte(r>>8), byte(g>>8), byte(bl>>8))
		}
	}

	var comp bytes.Buffer
	zw := zlib.NewWriter(&comp)
	if _, err := zw.Write(rgb); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}

	var buf bytes.Buffer
	offsets := make([]int, 6) // 1-indexed; index 0 unused

	buf.WriteString("%PDF-1.7\n")
	obj := func(n int, body string) {
		offsets[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}

	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, fmt.Sprintf(
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] "+
			"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>",
		width, height))

	content := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", width, height)
	obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))

	// Object 5 is the image XObject; write it by hand because it carries binary.
	offsets[5] = buf.Len()
	fmt.Fprintf(&buf,
		"5 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n",
		width, height, comp.Len())
	buf.Write(comp.Bytes())
	buf.WriteString("\nendstream\nendobj\n")

	// Cross-reference table and trailer.
	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefStart)

	_, err := w.Write(buf.Bytes())
	return err
}
