package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/singularityos-lab/torchio/internal/control"
	"github.com/singularityos-lab/torchio/internal/ipp"
)

// runPrinter implements `torchio printer <action>`.
//
//	torchio printer probe <uri>              query capabilities directly (no daemon)
//	torchio printer add <name> <uri> [--default]   register with the daemon
//	torchio printer ls                       list configured printers
//	torchio printer rm <name>                remove a printer
//	torchio printer default <name>           set the default printer
func runPrinter(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: torchio printer probe|add|ls|rm|default [args]")
	}
	switch args[0] {
	case "probe":
		return runProbe(ctx, args[1:])
	case "add":
		return printerAdd(args[1:])
	case "ls":
		return printerList(args[1:])
	case "rm":
		return printerRemove(args[1:])
	case "default":
		return printerDefault(args[1:])
	default:
		return fmt.Errorf("unknown printer action %q (probe, add, ls, rm, default)", args[0])
	}
}

func printerAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: torchio printer add <name> <uri> [--default]")
	}
	name, uri := args[0], args[1]
	makeDefault := false
	for _, a := range args[2:] {
		if a == "--default" {
			makeDefault = true
		} else {
			return fmt.Errorf("unknown flag %q", a)
		}
	}
	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()
	p, err := client.AddPrinter(control.AddPrinterArgs{Name: name, URI: uri, MakeDefault: makeDefault})
	if err != nil {
		return err
	}
	fmt.Printf("added %s (%s) supporting %s\n", p.Name, orUnknown(p.MakeAndModel), orNone(p.Formats))
	return nil
}

func printerList(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: torchio printer ls")
	}
	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()
	printers, err := client.ListPrinters()
	if err != nil {
		return err
	}
	if len(printers) == 0 {
		fmt.Println("no printers configured (add one with `torchio printer add`)")
		return nil
	}
	for _, p := range printers {
		marker := " "
		if p.Default {
			marker = "*"
		}
		fmt.Printf("%s %-16s %s  [%s]\n", marker, p.Name, p.URI, orNone(p.Formats))
	}
	return nil
}

func printerRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: torchio printer rm <name>")
	}
	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.RemovePrinter(args[0]); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}

func printerDefault(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: torchio printer default <name>")
	}
	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.SetDefault(args[0]); err != nil {
		return err
	}
	fmt.Printf("default printer is now %s\n", args[0])
	return nil
}

// runProbe fetches and pretty-prints a printer's capabilities. This is the
// command CUPS never had: one call that says exactly what a printer can do.
func runProbe(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: torchio printer probe <uri>")
	}
	uri := args[0]

	client := ipp.NewClient()
	msg, err := client.GetPrinterAttributes(ctx, uri)
	if err != nil {
		return fmt.Errorf("probe %s: %w", uri, err)
	}
	caps := ipp.ParseCapabilities(msg)

	fmt.Printf("Printer:    %s\n", orUnknown(caps.MakeAndModel))
	fmt.Printf("URI:        %s\n", uri)
	fmt.Printf("State:      %s\n", printerState(caps.State))
	if len(caps.StateReasons) > 0 {
		fmt.Printf("Reasons:    %s\n", strings.Join(caps.StateReasons, ", "))
	}
	fmt.Printf("Formats:    %s\n", joinFormats(caps.DocumentFormats))
	fmt.Printf("Media:      %s\n", orNone(caps.Media))
	fmt.Printf("Sides:      %s\n", orNone(caps.Sides))
	fmt.Printf("Resolution: %s\n", orNone(caps.Resolutions))
	fmt.Printf("Color:      %s\n", orNone(caps.ColorModes))
	fmt.Printf("Duplex:     %v\n", caps.SupportsDuplex())
	return nil
}

// printerState renders the IPP printer-state enum.
func printerState(state int) string {
	switch state {
	case 3:
		return "idle"
	case 4:
		return "processing"
	case 5:
		return "stopped"
	default:
		return fmt.Sprintf("unknown (%d)", state)
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "(none advertised)"
	}
	return strings.Join(items, ", ")
}
