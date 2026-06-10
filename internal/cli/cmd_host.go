package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/singularityos-lab/torchio/internal/host"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/transform"
)

// runHost implements `torchio host`: it hosts a legacy driver (a WASM module
// plus a JSON manifest) and exposes it as a local IPP printer. The driver
// converts the standard raster format the core sends into the printer's own
// language, which goes to the backend device.
//
//	torchio host --manifest driver.json --module driver.wasm --backend socket://printer.local:9100
//	torchio host --manifest driver.json --module driver.wasm --backend file:/dev/usb/lp0
func runHost(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	var (
		manifestPath = fs.String("manifest", "", "driver manifest JSON, required")
		modulePath   = fs.String("module", "", "driver WASM module, required")
		backendSpec  = fs.String("backend", "", "device backend: socket://host:port | file:/path | discard")
		listen       = fs.String("listen", "127.0.0.1:0", "address to expose the IPP printer on")
	)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *modulePath == "" || *backendSpec == "" {
		return fmt.Errorf("--manifest, --module and --backend are required")
	}

	mf, err := os.Open(*manifestPath)
	if err != nil {
		return fmt.Errorf("open manifest: %w", err)
	}
	manifest, err := host.ParseManifest(mf)
	mf.Close()
	if err != nil {
		return err
	}

	wasm, err := os.ReadFile(*modulePath)
	if err != nil {
		return fmt.Errorf("read module: %w", err)
	}
	driver, err := transform.NewWASMTransform(ctx, transform.Edge{
		From: manifest.PrimaryInput(),
		To:   model.MIME(manifest.Produces),
		Cost: 10,
	}, wasm)
	if err != nil {
		return err
	}
	defer driver.Close(ctx)

	backend, err := parseBackend(*backendSpec)
	if err != nil {
		return err
	}

	h := host.New(manifest, driver, backend)
	if err := h.Listen(*listen); err != nil {
		return err
	}
	fmt.Printf("hosting %q at %s\n", manifest.Name, h.URI())
	fmt.Println("add it with `torchio printer add`, Ctrl-C to stop")
	return h.Serve(ctx)
}

// parseBackend builds an output backend from a spec string.
func parseBackend(spec string) (host.Backend, error) {
	switch {
	case spec == "discard":
		return &host.BufferBackend{}, nil
	case strings.HasPrefix(spec, "socket://"):
		return host.SocketBackend{Addr: strings.TrimPrefix(spec, "socket://")}, nil
	case strings.HasPrefix(spec, "file:"):
		return host.FileBackend{Path: strings.TrimPrefix(spec, "file:")}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q (use socket://host:port, file:/path, or discard)", spec)
	}
}
