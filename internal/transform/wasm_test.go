package transform

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/singularityos-lab/torchio/internal/model"
)

// upperWASM builds the tiny WASI test module (source in testdata/upper, which
// uppercases stdin) and returns its bytes. Building it on demand keeps the
// compiled blob out of the repository; the test is skipped if the wasip1
// toolchain is unavailable.
func upperWASM(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "upper.wasm")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "testdata/upper"
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build wasip1 test module: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWASMTransform(t *testing.T) {
	ctx := context.Background()
	tr, err := NewWASMTransform(ctx, Edge{From: model.MIME("text/plain"), To: model.MIME("text/upper"), Cost: 5}, upperWASM(t))
	if err != nil {
		t.Fatalf("NewWASMTransform: %v", err)
	}
	defer tr.Close(ctx)

	if tr.Edge().To != model.MIME("text/upper") {
		t.Errorf("edge = %+v", tr.Edge())
	}

	// First run.
	var out bytes.Buffer
	if err := tr.Run(ctx, strings.NewReader("hello torchio"), &out, Params{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "HELLO TORCHIO" {
		t.Errorf("got %q, want %q", out.String(), "HELLO TORCHIO")
	}

	// The module must be re-instantiable for a second job.
	out.Reset()
	if err := tr.Run(ctx, strings.NewReader("second"), &out, Params{}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if out.String() != "SECOND" {
		t.Errorf("second run got %q", out.String())
	}
}

// TestWASMTransformInGraph confirms a WASM transform composes with the planner
// and registry exactly like a native one.
func TestWASMTransformInGraph(t *testing.T) {
	ctx := context.Background()
	tr, err := NewWASMTransform(ctx, Edge{From: model.MIME("text/plain"), To: model.MIME("text/upper"), Cost: 5}, upperWASM(t))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close(ctx)

	r := NewRegistry()
	r.Register(tr)
	plan, ok := r.Plan(model.MIME("text/plain"), []model.MIME{model.MIME("text/upper")})
	if !ok || len(plan) != 1 {
		t.Fatalf("plan = %v, ok = %v", plan, ok)
	}

	var out bytes.Buffer
	if err := plan[0].Run(ctx, strings.NewReader("via graph"), &out, Params{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "VIA GRAPH" {
		t.Errorf("got %q", out.String())
	}
}
