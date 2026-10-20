// Package daemon assembles torchiod, the per-user spooler. It holds no
// privilege: it runs in the user session and serves the control socket, the
// CUPS-compatible socket and the XDG print portal backend.
package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/singularityos-lab/torchio/internal/admin"
	"github.com/singularityos-lab/torchio/internal/control"
	"github.com/singularityos-lab/torchio/internal/cupscompat"
	"github.com/singularityos-lab/torchio/internal/discovery"
	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/paths"
	"github.com/singularityos-lab/torchio/internal/portal"
	"github.com/singularityos-lab/torchio/internal/printportal"
	"github.com/singularityos-lab/torchio/internal/queue"
	"github.com/singularityos-lab/torchio/internal/spool"
	"github.com/singularityos-lab/torchio/internal/store"
	"github.com/singularityos-lab/torchio/internal/transform"
)

// Daemon holds the running components so they can be shut down in order.
type Daemon struct {
	store    *store.Store
	spool    *spool.Spool
	engine   *queue.Engine
	server   *control.Server
	admin    *admin.Admin
	trust    portal.Portal
	recorder discovery.Recorder
	pdfium   *transform.PDFiumTransform
	log      *log.Logger

	printConn *dbus.Conn   // session bus for the XDG print portal, if available
	cupsSrv   *http.Server // CUPS-compat front end, if bound
	cupsLn    net.Listener
}

// Open wires the daemon from the per-user XDG paths but does not start serving.
func Open() (*Daemon, error) {
	dbPath, err := paths.DatabasePath()
	if err != nil {
		return nil, err
	}
	spoolDir, err := paths.SpoolDir()
	if err != nil {
		return nil, err
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	sp, err := spool.Open(spoolDir)
	if err != nil {
		st.Close()
		return nil, err
	}

	logger := log.Default()

	// pdfium initializes lazily, so a daemon that only prints PDFs to
	// PDF-capable printers never pays its start-up cost.
	reg := transform.DefaultRegistry()
	pdfiumTr := transform.NewPDFiumTransform(300)
	reg.Register(pdfiumTr)

	eng := queue.New(queue.Config{
		Store:      st,
		Spool:      sp,
		Transport:  ipp.NewClient(),
		Transforms: reg,
		Logger:     logger,
	})

	trust := selectPortal(logger)

	svc := control.NewService(st, eng, trust)
	srv, err := control.NewServer(svc)
	if err != nil {
		st.Close()
		return nil, err
	}

	rec := discovery.Recorder{Store: st, Log: logger}
	return &Daemon{store: st, spool: sp, engine: eng, server: srv, recorder: rec, admin: admin.New(st, eng), trust: trust, pdfium: pdfiumTr, log: logger}, nil
}

// selectPortal chooses the trust backend from $TORCHIO_TRUST_BACKEND, ush by
// default. Any failure falls back to deny, so a printer is never added and a
// certificate never trusted without a working backend.
func selectPortal(log *log.Logger) portal.Portal {
	backend := os.Getenv("TORCHIO_TRUST_BACKEND")
	if backend == "" {
		backend = portal.BackendUSH
	}
	policyFile := os.Getenv("TORCHIO_TRUST_POLICY")
	if policyFile == "" {
		if p, err := paths.TrustPolicyPath(); err == nil {
			policyFile = p
		}
	}
	p, err := portal.Select(backend, "", policyFile)
	if err != nil {
		log.Printf("trust backend %s unavailable, denying by default: %v", backend, err)
		return portal.AlwaysDeny{}
	}
	if backend == portal.BackendAllow {
		log.Print("trust backend is 'allow': every permission is granted (development only)")
	}
	return p
}

// startPrintPortal connects to the session bus and exports the XDG print portal
// backend. Every failure is non-fatal and only logged.
func (d *Daemon) startPrintPortal(ctx context.Context) {
	conn, err := dbus.SessionBus()
	if err != nil {
		d.log.Printf("XDG print portal disabled, no session bus: %v", err)
		return
	}
	backend := printportal.New(spoolerJobs{store: d.store, engine: d.engine})
	if err := printportal.Serve(ctx, conn, backend); err != nil {
		d.log.Printf("XDG print portal disabled: %v", err)
		_ = conn.Close()
		return
	}
	d.printConn = conn
	d.log.Print("XDG print portal active")
}

// cupsSocketPath returns where to expose the CUPS-compatible front end. A
// session that points libcups at a socket path through CUPS_SERVER gets
// torchiod on that same path.
func cupsSocketPath() string {
	if p := os.Getenv("TORCHIO_CUPS_SOCKET"); p != "" {
		return p
	}
	if p := os.Getenv("CUPS_SERVER"); filepath.IsAbs(p) {
		return p
	}
	return "/run/cups/cups.sock"
}

// startCupsCompat binds the CUPS socket and serves the libcups-compatible IPP
// front end. Every failure is non-fatal and only logged: without it, apps just
// keep using whatever print system they were, and Torchio's own CLI/portal
// still work.
func (d *Daemon) startCupsCompat(ctx context.Context) {
	sock := cupsSocketPath()
	_ = os.MkdirAll(filepath.Dir(sock), 0o700)
	// A leftover socket from a dead owner would block bind; clear only ours.
	if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(sock)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		d.log.Printf("CUPS socket disabled, cannot bind %s: %v", sock, err)
		return
	}
	_ = os.Chmod(sock, 0o666) // apps run as the user; the socket dir gates access

	compat := cupscompat.New(cupsBackend{store: d.store, engine: d.engine, admin: d.admin, trust: d.trust})
	if os.Getenv("TORCHIO_DEBUG") != "" {
		compat.Logf = func(f string, a ...any) { d.log.Printf("cups: "+f, a...) }
	}
	srv := &http.Server{Handler: compat}
	d.cupsSrv = srv
	d.cupsLn = ln
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			d.log.Printf("CUPS socket: %v", err)
		}
	}()
	d.log.Printf("CUPS socket on %s", sock)
}

// Run starts the engine and the control server and blocks until ctx is
// cancelled, then shuts everything down in order.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.engine.Start(ctx); err != nil {
		return fmt.Errorf("daemon: start engine: %w", err)
	}

	socket, err := paths.SocketPath()
	if err != nil {
		return err
	}
	if err := d.server.Listen(socket); err != nil {
		return err
	}
	d.log.Printf("listening on %s", socket)

	// Without multicast (no network, restricted sandbox) printers can still
	// be added by URI, so discovery failing is not fatal.
	go func() {
		if err := d.recorder.Run(ctx, discovery.MDNS{}, time.Now); err != nil {
			d.log.Printf("discovery disabled: %v", err)
		}
	}()

	d.startPrintPortal(ctx)
	d.startCupsCompat(ctx)

	serveErr := make(chan error, 1)
	go func() { serveErr <- d.server.Serve(ctx) }()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			d.shutdown()
			return fmt.Errorf("daemon: control server: %w", err)
		}
	}
	d.shutdown()
	return nil
}

// shutdown stops the components in reverse order of start.
func (d *Daemon) shutdown() {
	d.log.Print("shutting down")
	if d.printConn != nil {
		_ = d.printConn.Close()
	}
	if d.cupsSrv != nil {
		_ = d.cupsSrv.Close()
	}
	_ = d.server.Close()
	d.engine.Stop()
	if d.pdfium != nil {
		_ = d.pdfium.Close()
	}
	_ = d.store.Close()
}
