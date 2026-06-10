package transform

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"math"
)

// PWG Raster (PWG 5102.4) is the lingua franca of raster printers: IPP
// Everywhere requires a printer to accept it (or URF). This file encodes a
// sequence of RGB images into a PWG Raster stream so a rasterized PDF can print
// on a printer that does not take PDF. The format is a 4-byte sync word, then
// per page a fixed 1796-byte header followed by line-RLE-compressed pixels
// (the same CUPS-style packbits used by URF).

// pwgSync is the big-endian PWG/CUPS Raster version-2 sync word.
var pwgSync = []byte("RaS2")

// pwgColorSpaceSRGB is CUPS_CSPACE_SRGB.
const pwgColorSpaceSRGB = 19

// pwgHeaderSize is the fixed page-header length.
const pwgHeaderSize = 1796

// encodePWG writes a PWG Raster stream for the given RGB images at dpi. Every
// image is treated as 24-bit sRGB (3 bytes per pixel, chunky order).
func encodePWG(w io.Writer, pages []*image.RGBA, dpi int) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.Write(pwgSync); err != nil {
		return err
	}
	for _, img := range pages {
		if err := encodePWGPage(bw, img, dpi); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// encodePWGPage writes one page: the 1796-byte header then the RLE raster data.
func encodePWGPage(w *bufio.Writer, img *image.RGBA, dpi int) error {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("pwg: empty page")
	}

	if _, err := w.Write(pwgPageHeader(width, height, dpi)); err != nil {
		return err
	}

	// Pack the image into a tight width*height*3 RGB buffer (drop alpha) so we
	// can compare and RLE whole pixels.
	const ps = 3
	rgb := make([]byte, width*height*ps)
	for y := 0; y < height; y++ {
		row := rgb[y*width*ps:]
		src := img.PixOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < width; x++ {
			row[x*ps+0] = img.Pix[src+x*4+0]
			row[x*ps+1] = img.Pix[src+x*4+1]
			row[x*ps+2] = img.Pix[src+x*4+2]
		}
	}

	// Line-level run length: collapse consecutive identical lines.
	lineBytes := width * ps
	y := 0
	for y < height {
		reps := 1
		for y+reps < height && reps < 256 &&
			equalBytes(rgb[(y)*lineBytes:(y+1)*lineBytes], rgb[(y+reps)*lineBytes:(y+reps+1)*lineBytes]) {
			reps++
		}
		if err := w.WriteByte(byte(reps - 1)); err != nil {
			return err
		}
		if err := encodePWGLine(w, rgb[y*lineBytes:(y+1)*lineBytes], ps); err != nil {
			return err
		}
		y += reps
	}
	return nil
}

// encodePWGLine emits one line with CUPS/PWG packbits at pixel granularity:
//   - a count byte 0..127 means: repeat the next pixel (count+1) times;
//   - a count byte 129..255 means: copy the next (257-count) pixels literally;
//   - 128 is reserved and never emitted.
func encodePWGLine(w *bufio.Writer, line []byte, ps int) error {
	n := len(line) / ps
	i := 0
	for i < n {
		// Length of the repeat run starting at i (capped at 128).
		runEnd := i + 1
		for runEnd < n && runEnd-i < 128 && equalPixel(line, i, runEnd, ps) {
			runEnd++
		}
		if runEnd-i >= 2 {
			if err := w.WriteByte(byte(runEnd - i - 1)); err != nil {
				return err
			}
			if _, err := w.Write(line[i*ps : (i+1)*ps]); err != nil {
				return err
			}
			i = runEnd
			continue
		}

		// Otherwise gather a literal run until a repeat (>=2) begins or 128.
		litStart := i
		i++
		for i < n && i-litStart < 128 {
			if i+1 < n && equalPixel(line, i, i+1, ps) {
				break
			}
			i++
		}
		litLen := i - litStart
		if litLen == 1 {
			// A lone pixel is cheapest as a single repeat.
			if err := w.WriteByte(0); err != nil {
				return err
			}
			if _, err := w.Write(line[litStart*ps : (litStart+1)*ps]); err != nil {
				return err
			}
			continue
		}
		if err := w.WriteByte(byte(257 - litLen)); err != nil {
			return err
		}
		if _, err := w.Write(line[litStart*ps : i*ps]); err != nil {
			return err
		}
	}
	return nil
}

// pwgPageHeader builds the 1796-byte page header. Only the fields PWG Raster
// requires are set; the rest stay zero. Integers and floats are big-endian
// (the "RaS2" byte order).
func pwgPageHeader(width, height, dpi int) []byte {
	h := make([]byte, pwgHeaderSize)
	u32 := func(off int, v uint32) { binary.BigEndian.PutUint32(h[off:], v) }
	f32 := func(off int, v float32) { binary.BigEndian.PutUint32(h[off:], math.Float32bits(v)) }

	// Offsets within cups_page_header2_t. The first 256 bytes are four 64-byte
	// strings (MediaClass/MediaColor/MediaType/OutputType), left zero. Offsets
	// are computed step by step to match the struct layout exactly.
	off := 256
	off += 5 * 4            // AdvanceDistance, AdvanceMedia, Collate, CutMedia, Duplex
	u32(off, uint32(dpi))   // HWResolution[0]
	u32(off+4, uint32(dpi)) // HWResolution[1]
	off += 2 * 4            // HWResolution
	off += 4 * 4            // ImagingBoundingBox[4]
	off += 3 * 4            // InsertSheet, Jog, LeadingEdge
	off += 2 * 4            // Margins[2]
	off += 8 * 4            // ManualFeed..OutputFaceUp (8 u32)
	off += 2 * 4            // PageSize[2]
	off += 3 * 4            // Separations, TraySwitch, Tumble
	// Now at cupsWidth.
	cupsWidth := off
	u32(cupsWidth+0, uint32(width))    // cupsWidth
	u32(cupsWidth+4, uint32(height))   // cupsHeight
	u32(cupsWidth+8, 0)                // cupsMediaType
	u32(cupsWidth+12, 8)               // cupsBitsPerColor
	u32(cupsWidth+16, 24)              // cupsBitsPerPixel
	u32(cupsWidth+20, uint32(width*3)) // cupsBytesPerLine
	u32(cupsWidth+24, 0)               // cupsColorOrder (chunky)
	u32(cupsWidth+28, pwgColorSpaceSRGB)
	u32(cupsWidth+32, 0) // cupsCompression
	u32(cupsWidth+36, 0) // cupsRowCount
	u32(cupsWidth+40, 0) // cupsRowFeed
	u32(cupsWidth+44, 0) // cupsRowStep
	u32(cupsWidth+48, 3) // cupsNumColors
	off = cupsWidth + 52
	f32(off, 1.0) // cupsBorderlessScalingFactor
	off += 4
	// cupsPageSize[2] in points (72 per inch).
	f32(off, float32(width)*72/float32(dpi))
	f32(off+4, float32(height)*72/float32(dpi))
	return h
}

// --- decoding (used by the round-trip test; printers do the real decoding) ---

// pwgPage is a decoded page.
type pwgPage struct {
	Width, Height int
	RGB           []byte // width*height*3
}

// decodePWG parses a PWG Raster stream back into pages. It exists so the
// encoder can be validated by round-trip; a printer is the real consumer.
func decodePWG(data []byte) ([]pwgPage, error) {
	if len(data) < 4 || string(data[:4]) != string(pwgSync) {
		return nil, fmt.Errorf("pwg: bad sync word")
	}
	r := data[4:]
	var pages []pwgPage
	for len(r) > 0 {
		if len(r) < pwgHeaderSize {
			return nil, fmt.Errorf("pwg: truncated header")
		}
		hdr := r[:pwgHeaderSize]
		r = r[pwgHeaderSize:]
		cupsWidth := 256 + 5*4 + 2*4 + 4*4 + 3*4 + 2*4 + 8*4 + 2*4 + 3*4
		width := int(binary.BigEndian.Uint32(hdr[cupsWidth:]))
		height := int(binary.BigEndian.Uint32(hdr[cupsWidth+4:]))
		const ps = 3
		page := pwgPage{Width: width, Height: height, RGB: make([]byte, 0, width*height*ps)}

		linesDone := 0
		for linesDone < height {
			if len(r) == 0 {
				return nil, fmt.Errorf("pwg: truncated raster")
			}
			reps := int(r[0]) + 1
			r = r[1:]
			line, rest, err := decodePWGLine(r, width, ps)
			if err != nil {
				return nil, err
			}
			r = rest
			for k := 0; k < reps && linesDone < height; k++ {
				page.RGB = append(page.RGB, line...)
				linesDone++
			}
		}
		pages = append(pages, page)
	}
	return pages, nil
}

func decodePWGLine(r []byte, width, ps int) (line, rest []byte, err error) {
	line = make([]byte, 0, width*ps)
	for len(line)/ps < width {
		if len(r) == 0 {
			return nil, nil, fmt.Errorf("pwg: truncated line")
		}
		c := r[0]
		r = r[1:]
		switch {
		case c < 128: // repeat next pixel c+1 times
			if len(r) < ps {
				return nil, nil, fmt.Errorf("pwg: truncated repeat pixel")
			}
			px := r[:ps]
			r = r[ps:]
			for k := 0; k <= int(c); k++ {
				line = append(line, px...)
			}
		case c > 128: // literal: copy (257-c) pixels
			cnt := 257 - int(c)
			if len(r) < cnt*ps {
				return nil, nil, fmt.Errorf("pwg: truncated literal")
			}
			line = append(line, r[:cnt*ps]...)
			r = r[cnt*ps:]
		default: // 128 reserved
			return nil, nil, fmt.Errorf("pwg: reserved count byte 128")
		}
	}
	return line, r, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalPixel(line []byte, i, j, ps int) bool {
	for k := 0; k < ps; k++ {
		if line[i*ps+k] != line[j*ps+k] {
			return false
		}
	}
	return true
}
