// Package cli implements the multi-call dispatch for the torchio binary
// (principle P5). The same executable is reachable as `torchio <subcommand>`
// or through the lp/lpr/lpstat symlinks; this package maps both onto the
// internal subcommands. Subcommands land milestone by milestone.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// command is one entry of the multi-call binary.
type command struct {
	name      string
	milestone string // roadmap milestone that fills this in
	summary   string
	run       func(ctx context.Context, args []string) error
}

// commands is the registry of torchio subcommands, keyed by name.
var commands = map[string]command{
	"print":    {"print", "M0", "send a document to an IPP printer (passthrough)", runPrint},
	"printer":  {"printer", "M1", "add, list, remove, probe and set default printers", runPrinter},
	"daemon":   {"daemon", "M1", "run the per-user spooler (torchiod)", runDaemon},
	"queue":    {"queue", "M1", "list, cancel and inspect jobs", runQueue},
	"discover": {"discover", "M2", "list and confirm printers found on the network", runDiscover},
	"doctor":   {"doctor", "M2", "end-to-end diagnostics of the print path", runDoctor},
	"usb":      {"usb", "M4", "expose USB printers as local IPP endpoints (IPP-over-USB)", runUsb},
	"host":     {"host", "M5", "host a legacy WASM driver as a local IPP printer", runHost},
	"share":    {"share", "M6", "share a printer on the network over IPPS", runShare},
	"scan":     {"scan", "M6", "scan a page from a network (eSCL/AirScan) scanner", runScan},
}

// shims maps the legacy program names (when invoked via symlink) to a torchio
// subcommand. They translate the sane subset of historical options.
var shims = map[string]string{
	"lp":     "print",
	"lpr":    "print",
	"lpstat": "queue",
}

// Dispatch routes os.Args to the right subcommand. It resolves the legacy
// shims first (by argv[0]), then the explicit `torchio <subcommand>` form. It
// installs a context cancelled on SIGINT/SIGTERM so in-flight prints abort
// cleanly.
func Dispatch(args []string) error {
	// Catch SIGINT (Ctrl-C) and SIGTERM (systemd stop) so the daemon shuts
	// down cleanly and removes its socket.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) == 0 {
		return usage()
	}

	prog := filepath.Base(args[0])
	if sub, ok := shims[prog]; ok {
		return commands[sub].run(ctx, args[1:])
	}

	if len(args) < 2 {
		return usage()
	}

	sub := args[1]
	if sub == "-h" || sub == "--help" || sub == "help" {
		return usage()
	}

	cmd, ok := commands[sub]
	if !ok {
		return fmt.Errorf("unknown subcommand %q (try `torchio help`)", sub)
	}
	return cmd.run(ctx, args[2:])
}

// usage prints the multi-call command list. Order is roadmap-ish, not map order.
func usage() error {
	fmt.Println("torchio: a modern, IPP-only print system")
	fmt.Println()
	fmt.Println("usage: torchio <subcommand> [args]")
	fmt.Println()
	fmt.Println("subcommands:")
	for _, name := range []string{"print", "printer", "daemon", "queue", "discover", "doctor", "usb", "host", "share", "scan"} {
		c := commands[name]
		fmt.Printf("  %-8s [%s]  %s\n", c.name, c.milestone, c.summary)
	}
	fmt.Println()
	fmt.Println("compatibility shims (via symlink): lp, lpr -> print; lpstat -> queue")
	return nil
}
