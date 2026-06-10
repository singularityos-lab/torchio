package transform

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// WASMTransform runs a WebAssembly module as a format converter.
// The module is a WASI command that reads the input document from stdin and
// writes the output to stdout; it sees nothing else: no filesystem, no network,
// no clock. This is the sandbox in which hostile input (a malformed PDF fed to
// pdfium) is parsed, so a code-execution bug in the module gains control of
// only a linear memory with no syscalls. Modules are architecture-independent:
// the same .wasm runs on x86_64, aarch64 and riscv64.
type WASMTransform struct {
	edge     Edge
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
}

// NewWASMTransform compiles a .wasm module and binds it to a graph edge (its
// from/to formats and cost come from the module's manifest). The returned
// transform must be Closed when no longer needed.
func NewWASMTransform(ctx context.Context, edge Edge, wasm []byte) (*WASMTransform, error) {
	rt := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("transform: compile wasm: %w", err)
	}
	return &WASMTransform{edge: edge, runtime: rt, compiled: compiled}, nil
}

// Edge returns the module's place in the format graph.
func (w *WASMTransform) Edge() Edge { return w.edge }

// Run instantiates the module with the document streams wired to stdin/stdout
// and executes it. The module is given an empty name so it can be instantiated
// repeatedly (once per job) without colliding in the runtime namespace.
func (w *WASMTransform) Run(ctx context.Context, in io.Reader, out io.Writer, _ Params) error {
	config := wazero.NewModuleConfig().
		WithName("").
		WithStdin(in).
		WithStdout(out).
		WithStderr(io.Discard).
		WithArgs("transform")

	mod, err := w.runtime.InstantiateModule(ctx, w.compiled, config)
	if mod != nil {
		_ = mod.Close(ctx)
	}
	if err != nil {
		// A WASI command that finishes by calling proc_exit returns a
		// *sys.ExitError; exit code 0 is a normal, successful run.
		var exit *sys.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 0 {
				return nil
			}
			return fmt.Errorf("transform: wasm module exited with code %d", exit.ExitCode())
		}
		return fmt.Errorf("transform: run wasm: %w", err)
	}
	return nil
}

// Close releases the runtime and the compiled module.
func (w *WASMTransform) Close(ctx context.Context) error {
	return w.runtime.Close(ctx)
}
