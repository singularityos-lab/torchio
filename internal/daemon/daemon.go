// Package daemon assembles torchiod, the per-user spooler. It is built on
// go-foundation's hosting host: the store, spool, event bus and queue engine
// are wired once, the engine and the control server run as background services,
// and a clean shutdown drains them. There is no privilege here; the daemon runs
// in the user session and its only API is the control socket (principle P4).
package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/mirkobrombin/go-foundation/pkg/events"
	"github.com/mirkobrombin/go-foundation/pkg/logger"

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
	recorder discovery.Recorder
	pdfium   *transform.PDFiumTransform
	log      logger.Logger

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

	log := logger.New()
	bus := events.New()

	// Transform registry: the native (pure-Go) transforms plus pdfium for
	// PDF -> PWG raster. pdfium initializes lazily on first use, so a daemon
	// that only prints PDFs to PDF-capable printers never pays its cost.
	reg := transform.DefaultRegistry()
	pdfiumTr := transform.NewPDFiumTransform(300)
	reg.Register(pdfiumTr)

	eng := queue.New(queue.Config{
		Store:      st,
		Spool:      sp,
		Bus:        bus,
		Transport:  ipp.NewClient(),
		Transforms: reg,
		Logger:     log,
	})

	// Trust decisions go to the ush portal over the session bus. With no
	// session bus (e.g. a headless run) we fall back to deny-by-default: a
	// printer is never added without a human, which is the safe choice.
	trust := selectPortal(log)

	svc := control.NewService(st, eng, trust)
	srv, err := control.NewServer(svc)
	if err != nil {
		st.Close()
		return nil, err
	}

	rec := discovery.Recorder{Store: st, Log: log}
	return &Daemon{store: st, spool: sp, engine: eng, server: srv, recorder: rec, pdfium: pdfiumTr, log: log}, nil
}

// selectPortal chooses the trust backend: the ush D-Bus portal when a session
// bus is reachable, otherwise deny-by-default.
func selectPortal(log logger.Logger) portal.Portal {
	p, err := portal.NewDBus("")
	if err != nil {
		log.Warn("no session bus for the ush portal; trust decisions will be denied: " + err.Error())
		return portal.AlwaysDeny{}
	}
	return p
}

// startPrintPortal connects to the session bus and exports the XDG print portal
// backend. Every failure is non-fatal and only logged.
func (d *Daemon) startPrintPortal(ctx context.Context) {
	conn, err := dbus.SessionBus()
	if err != nil {
		d.log.Warn("XDG print portal disabled (no session bus): " + err.Error())
		return
	}
	backend := printportal.New(spoolerJobs{store: d.store, engine: d.engine})
	if err := printportal.Serve(ctx, conn, backend); err != nil {
		d.log.Warn("XDG print portal disabled: " + err.Error())
		_ = conn.Close()
		return
	}
	d.printConn = conn
	d.log.Info("XDG print portal active", logger.Field{Key: "bus", Value: "org.freedesktop.impl.portal.desktop.torchio"})
}

// cupsSocketPath returns where to expose the CUPS-compatible front end:
// $TORCHIO_CUPS_SOCKET if set, else the well-known CUPS socket.
func cupsSocketPath() string {
	if p := os.Getenv("TORCHIO_CUPS_SOCKET"); p != "" {
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
	// A leftover socket from a dead owner would block bind; clear only ours.
	if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(sock)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		d.log.Warn("CUPS-compat disabled (cannot bind " + sock + "): " + err.Error())
		return
	}
	_ = os.Chmod(sock, 0o666) // apps run as the user; the socket dir gates access

	srv := &http.Server{Handler: cupscompat.New(cupsBackend{store: d.store, engine: d.engine})}
	d.cupsSrv = srv
	d.cupsLn = ln
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			d.log.Warn("CUPS-compat server: " + err.Error())
		}
	}()
	d.log.Info("CUPS-compat active", logger.Field{Key: "socket", Value: sock})
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
	d.log.Info("torchiod listening", logger.Field{Key: "socket", Value: socket})

	// Start read-only network discovery. A failure to open the multicast
	// socket (restricted environments, no network) is non-fatal: the daemon
	// runs fine without discovery, and printers can still be added by URI.
	go func() {
		if err := d.recorder.Run(ctx, discovery.MDNS{}, time.Now); err != nil {
			d.log.Warn("discovery disabled: " + err.Error())
		}
	}()

	// Start the XDG print portal backend so sandboxed GTK apps print through
	// Torchio. Non-fatal: with no session bus, apps simply do not get this
	// integration, and CLI printing still works.
	d.startPrintPortal(ctx)

	// Start the CUPS-compatible front end so existing libcups apps (GTK/Qt
	// dialogs, lp/lpstat, Chromium) see Torchio's queues as CUPS queues.
	// Non-fatal: binding the CUPS socket needs cupsd not to own it and the
	// socket dir to be writable.
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
	d.log.Info("torchiod shutting down")
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
