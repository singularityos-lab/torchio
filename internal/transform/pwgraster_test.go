package transform

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

// rgbOf extracts the tight width*height*3 RGB bytes of an image, matching what
// the encoder packs, so tests can compare against the decoded output.
func rgbOf(img *image.RGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, 0, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			out = append(out, img.Pix[off+0], img.Pix[off+1], img.Pix[off+2])
		}
	}
	return out
}

// roundTrip encodes then decodes and asserts the pixels survive exactly.
func roundTrip(t *testing.T, img *image.RGBA) {
	t.Helper()
	var buf bytes.Buffer
	if err := encodePWG(&buf, []*image.RGBA{img}, 300); err != nil {
		t.Fatalf("encodePWG: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("RaS2")) {
		t.Fatal("missing RaS2 sync word")
	}
	pages, err := decodePWG(buf.Bytes())
	if err != nil {
		t.Fatalf("decodePWG: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	b := img.Bounds()
	if pages[0].Width != b.Dx() || pages[0].Height != b.Dy() {
		t.Fatalf("size = %dx%d, want %dx%d", pages[0].Width, pages[0].Height, b.Dx(), b.Dy())
	}
	if !bytes.Equal(pages[0].RGB, rgbOf(img)) {
		t.Errorf("pixels did not survive the round trip")
	}
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestPWGRoundTripSolid(t *testing.T) {
	// Solid color exercises both pixel-repeat runs and line-repeat runs.
	roundTrip(t, solid(40, 30, color.RGBA{R: 10, G: 200, B: 50, A: 255}))
}

func TestPWGRoundTripGradient(t *testing.T) {
	// A gradient is mostly literal runs with occasional repeats.
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 5), B: uint8((x + y) * 2), A: 255})
		}
	}
	roundTrip(t, img)
}

func TestPWGRoundTripPseudoRandom(t *testing.T) {
	// Deterministic pseudo-random pixels stress the literal/repeat boundaries.
	img := image.NewRGBA(image.Rect(0, 0, 33, 17))
	s := uint32(12345)
	for y := 0; y < 17; y++ {
		for x := 0; x < 33; x++ {
			s = s*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{R: byte(s), G: byte(s >> 8), B: byte(s >> 16), A: 255})
		}
	}
	roundTrip(t, img)
}

func TestPWGRoundTripThinStrips(t *testing.T) {
	roundTrip(t, solid(1, 50, color.RGBA{A: 255}))
	roundTrip(t, solid(50, 1, color.RGBA{R: 255, A: 255}))
	roundTrip(t, solid(1, 1, color.RGBA{B: 255, A: 255}))
}

// TestPWGHeader checks the page header is the right size and carries the
// dimensions and resolution at the expected offsets.
func TestPWGHeader(t *testing.T) {
	h := pwgPageHeader(1241, 1754, 150)
	if len(h) != pwgHeaderSize {
		t.Fatalf("header size = %d, want %d", len(h), pwgHeaderSize)
	}
	// HWResolution[0] right after the 256 string bytes + 5 u32.
	hwRes := 256 + 5*4
	if got := binary.BigEndian.Uint32(h[hwRes:]); got != 150 {
		t.Errorf("HWResolution = %d, want 150", got)
	}
	cupsWidth := 256 + 5*4 + 2*4 + 4*4 + 3*4 + 2*4 + 8*4 + 2*4 + 3*4
	if w := binary.BigEndian.Uint32(h[cupsWidth:]); w != 1241 {
		t.Errorf("cupsWidth = %d, want 1241", w)
	}
	if hh := binary.BigEndian.Uint32(h[cupsWidth+4:]); hh != 1754 {
		t.Errorf("cupsHeight = %d, want 1754", hh)
	}
	if bpp := binary.BigEndian.Uint32(h[cupsWidth+16:]); bpp != 24 {
		t.Errorf("cupsBitsPerPixel = %d, want 24", bpp)
	}
	if bpl := binary.BigEndian.Uint32(h[cupsWidth+20:]); bpl != 1241*3 {
		t.Errorf("cupsBytesPerLine = %d, want %d", bpl, 1241*3)
	}
}

// TestPWGLiteralEncoding hand-verifies the RLE on a known line: three distinct
// pixels followed by three identical ones.
func TestPWGLiteralEncoding(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 6, 1))
	img.SetRGBA(0, 0, color.RGBA{1, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{2, 0, 0, 255})
	img.SetRGBA(2, 0, color.RGBA{3, 0, 0, 255})
	img.SetRGBA(3, 0, color.RGBA{9, 0, 0, 255})
	img.SetRGBA(4, 0, color.RGBA{9, 0, 0, 255})
	img.SetRGBA(5, 0, color.RGBA{9, 0, 0, 255})
	roundTrip(t, img)
}
