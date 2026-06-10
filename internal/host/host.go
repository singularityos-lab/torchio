package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/singularityos-lab/torchio/internal/transform"
)

// Host ties a driver manifest, its WASM module and an output backend into a
// local IPP printer. The core sends a standard format (e.g. PWG raster, after
// rasterizing a PDF) to ipp://127.0.0.1:PORT; the host runs the driver
// module to turn it into the printer's language and writes that to the device.
type Host struct {
	manifest *Manifest
	driver   transform.Transform // WASM module: accepts -> produces
	backend  Backend

	server *Server
	http   *http.Server
	ln     net.Listener
}

// New builds a host for a manifest, driver module and backend.
func New(m *Manifest, driver transform.Transform, backend Backend) *Host {
	h := &Host{manifest: m, driver: driver, backend: backend}
	h.server = NewServer(m.Name, m.IPPCapabilities(), m.PrimaryInput(), h.submit)
	h.http = &http.Server{Handler: h.server}
	return h
}

// submit is the per-job pipeline: run the driver module over the received
// document, then write its output to the device.
func (h *Host) submit(ctx context.Context, _ string, doc io.Reader) error {
	var out bytes.Buffer
	if err := h.driver.Run(ctx, doc, &out, transform.Params{}); err != nil {
		return fmt.Errorf("host: driver %s: %w", h.manifest.Name, err)
	}
	return h.backend.Send(ctx, &out)
}

// Listen binds the loopback IPP socket. Use "127.0.0.1:0" for an ephemeral port.
func (h *Host) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("host: listen: %w", err)
	}
	h.ln = ln
	return nil
}

// Addr returns the bound host:port.
func (h *Host) Addr() string {
	if h.ln == nil {
		return ""
	}
	return h.ln.Addr().String()
}

// URI returns the ipp:// URI the core uses to reach this hosted printer.
func (h *Host) URI() string {
	return "ipp://" + h.Addr() + "/ipp/print"
}

// Serve handles IPP requests until ctx is cancelled.
func (h *Host) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = h.http.Close()
	}()
	err := h.http.Serve(h.ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Close stops the server.
func (h *Host) Close() error {
	return h.http.Close()
}
