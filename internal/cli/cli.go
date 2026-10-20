// Package cli implements the torchio client. The same executable answers as
// `torchio <subcommand>` and, through symlinks, as lp, lpr and lpstat.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string) error
}

var commands = map[string]command{
	"print":    {"print", "send a document to an IPP printer (passthrough)", runPrint},
	"printer":  {"printer", "add, list, remove, probe and set default printers", runPrinter},
	"queue":    {"queue", "list, cancel and inspect jobs", runQueue},
	"discover": {"discover", "list and confirm printers found on the network", runDiscover},
	"doctor":   {"doctor", "end-to-end diagnostics of the print path", runDoctor},
	"usb":      {"usb", "expose USB printers as local IPP endpoints (IPP-over-USB)", runUsb},
	"host":     {"host", "host a legacy WASM driver as a local IPP printer", runHost},
	"share":    {"share", "share a printer on the network over IPPS", runShare},
	"scan":     {"scan", "scan a page from a network (eSCL/AirScan) scanner", runScan},
}

// shims maps the program name, when invoked through a symlink, to a subcommand.
var shims = map[string]string{
	"lp":     "print",
	"lpr":    "print",
	"lpstat": "queue",
}

// Dispatch routes os.Args to a subcommand, by program name first and then by
// the first argument. In-flight prints abort on SIGINT and SIGTERM.
func Dispatch(args []string) error {
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

func usage() error {
	fmt.Println("usage: torchio <subcommand> [args]")
	fmt.Println()
	fmt.Println("subcommands:")
	for _, name := range []string{"print", "printer", "queue", "discover", "doctor", "usb", "host", "share", "scan"} {
		c := commands[name]
		fmt.Printf("  %-9s %s\n", c.name, c.summary)
	}
	fmt.Println()
	fmt.Println("installed as lp or lpr it prints; as lpstat it lists jobs")
	return nil
}
