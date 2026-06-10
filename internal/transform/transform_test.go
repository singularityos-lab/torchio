package transform

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/singularityos-lab/torchio/internal/detect"
	"github.com/singularityos-lab/torchio/internal/model"
)

// fakeT is a transform with no behavior, for graph tests.
type fakeT struct{ e Edge }

func (f fakeT) Edge() Edge                                              { return f.e }
func (f fakeT) Run(context.Context, io.Reader, io.Writer, Params) error { return nil }

// edge is a tiny helper to register a synthetic edge.
func edge(from, to model.MIME, cost int) Transform {
	return fakeT{e: Edge{From: from, To: to, Cost: cost}}
}

func TestPlanCheapestPath(t *testing.T) {
	const A, B, C model.MIME = "a", "b", "c"
	r := NewRegistry()
	r.Register(edge(A, B, 1))
	r.Register(edge(B, C, 1))
	r.Register(edge(A, C, 5)) // direct but expensive

	plan, ok := r.Plan(A, []model.MIME{C})
	if !ok {
		t.Fatal("expected a plan")
	}
	if len(plan) != 2 {
		t.Fatalf("expected 2 hops (A->B->C is cheaper than A->C), got %d", len(plan))
	}
	if plan[0].Edge().From != A || plan[1].Edge().To != C {
		t.Errorf("path = %v", plan)
	}
	if got := FinalFormat(plan, A); got != C {
		t.Errorf("final format = %v, want %v", got, C)
	}
}

func TestPlanFastPath(t *testing.T) {
	const A model.MIME = "a"
	r := NewRegistry()
	plan, ok := r.Plan(A, []model.MIME{A, "b"})
	if !ok {
		t.Fatal("format already accepted should plan ok")
	}
	if len(plan) != 0 {
		t.Errorf("fast path must be an empty plan, got %d hops", len(plan))
	}
	if got := FinalFormat(plan, A); got != A {
		t.Errorf("final format = %v, want %v", got, A)
	}
}

func TestPlanNoPath(t *testing.T) {
	const A, B, D model.MIME = "a", "b", "d"
	r := NewRegistry()
	r.Register(edge(A, B, 1))
	if _, ok := r.Plan(A, []model.MIME{D}); ok {
		t.Error("expected no path from a to d")
	}
}

func TestPlanNearestTarget(t *testing.T) {
	const A, B, C, X model.MIME = "a", "b", "c", "x"
	r := NewRegistry()
	r.Register(edge(A, B, 1))
	r.Register(edge(B, C, 1))
	r.Register(edge(A, X, 2))
	// Targets B (cost 1) and X (cost 2): the planner should pick B.
	plan, ok := r.Plan(A, []model.MIME{X, B})
	if !ok || len(plan) != 1 || plan[0].Edge().To != B {
		t.Errorf("expected single hop to nearest target B, got %v", plan)
	}
}

func TestImageToPDF(t *testing.T) {
	// Build a small PNG.
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 60), G: uint8(y * 80), B: 128, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}

	tr := imageToPDF{from: model.MIMEPNG}
	if tr.Edge().To != model.MIMEPDF {
		t.Fatalf("edge target = %v", tr.Edge().To)
	}

	var pdf bytes.Buffer
	if err := tr.Run(context.Background(), &pngBuf, &pdf, Params{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := pdf.Bytes()
	if got := detect.FormatBytes(out); got != model.MIMEPDF {
		t.Errorf("output not detected as PDF: %v", got)
	}
	for _, want := range []string{"%PDF-1.7", "/Subtype /Image", "/Filter /FlateDecode", "startxref", "%%EOF"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("PDF missing %q", want)
		}
	}
}
