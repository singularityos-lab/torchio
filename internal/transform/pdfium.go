package transform

import (
	"context"
	"fmt"
	"image"
	"io"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"

	"github.com/singularityos-lab/torchio/internal/model"
)

// PDFiumTransform rasterizes a PDF into PWG Raster using pdfium compiled to
// WebAssembly and executed in wazero (the approach the design doc prescribes:
// the Chromium rasterizer, in a sandbox, with no CGo). pdfium is the one place
// hostile input (a malformed PDF) is parsed, and here it runs with no
// filesystem, no network and no syscalls beyond what WASI exposes.
//
// The pdfium pool is heavy (it compiles a ~5 MB wasm), so it is created lazily
// on first use: a daemon that only ever prints PDFs to PDF-capable printers
// never pays for it.
type PDFiumTransform struct {
	dpi int

	once    sync.Once
	pool    pdfium.Pool
	initErr error
}

// NewPDFiumTransform returns a PDF -> PWG-raster transform rendering at the
// given DPI (300 if dpi <= 0).
func NewPDFiumTransform(dpi int) *PDFiumTransform {
	if dpi <= 0 {
		dpi = 300
	}
	return &PDFiumTransform{dpi: dpi}
}

// Edge places the transform in the graph at a high cost: rasterizing is the
// last resort, taken only when the printer cannot accept PDF.
func (t *PDFiumTransform) Edge() Edge {
	return Edge{From: model.MIMEPDF, To: model.MIMEPWGRaster, Cost: 100}
}

func (t *PDFiumTransform) init() {
	t.once.Do(func() {
		pool, err := webassembly.Init(webassembly.Config{
			MinIdle:  1,
			MaxIdle:  1,
			MaxTotal: 1,
			// Empty FS config: pdfium sees no host filesystem (sandbox).
			FSConfig: wazero.NewFSConfig(),
		})
		if err != nil {
			t.initErr = err
			return
		}
		t.pool = pool
	})
}

// Run reads the whole PDF, renders every page to an RGB image and encodes the
// pages as a PWG Raster stream.
func (t *PDFiumTransform) Run(_ context.Context, in io.Reader, out io.Writer, _ Params) error {
	t.init()
	if t.initErr != nil {
		return fmt.Errorf("transform: pdfium init: %w", t.initErr)
	}

	pdf, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("transform: read pdf: %w", err)
	}

	inst, err := t.pool.GetInstance(60 * time.Second)
	if err != nil {
		return fmt.Errorf("transform: pdfium instance: %w", err)
	}
	defer inst.Close()

	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &pdf})
	if err != nil {
		return fmt.Errorf("transform: open pdf: %w", err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})

	pc, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return fmt.Errorf("transform: page count: %w", err)
	}

	pages := make([]*image.RGBA, 0, pc.PageCount)
	for i := 0; i < pc.PageCount; i++ {
		res, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
			Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}},
			DPI:  t.dpi,
		})
		if err != nil {
			return fmt.Errorf("transform: render page %d: %w", i, err)
		}
		// The rendered image may be backed by wasm memory that Cleanup frees;
		// copy it into a standalone image before releasing.
		pages = append(pages, cloneRGBA(res.Result.Image))
		res.Cleanup()
	}

	if len(pages) == 0 {
		return fmt.Errorf("transform: pdf has no pages")
	}
	return encodePWG(out, pages, t.dpi)
}

// Close releases the pdfium pool. Safe to call on a never-initialized transform.
func (t *PDFiumTransform) Close() error {
	if t.pool != nil {
		return t.pool.Close()
	}
	return nil
}

// cloneRGBA makes a standalone copy of an image so it survives pdfium cleanup.
func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}
