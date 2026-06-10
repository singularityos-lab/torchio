package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/singularityos-lab/torchio/internal/control"
)

// runDiscover implements `torchio discover`, the front end to read-only network
// discovery.
//
//	torchio discover                 list printers found on the network
//	torchio discover add <key> <name> [--default]
//	                                 confirm one as a printer (asks the portal)
func runDiscover(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "add" {
		return discoverAdd(args[1:])
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: torchio discover [add <key> <name>]")
	}
	return discoverList()
}

func discoverList() error {
	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()

	devices, err := client.ListDiscovered()
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		fmt.Println("no printers found on the network yet (discovery is read-only; nothing is added automatically)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tNAME\tURI")
	for _, d := range devices {
		fmt.Fprintf(w, "%s\t%s\t%s\n", d.Key, d.Name, d.URI)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Println("\nconfirm one with: torchio discover add <key> <name>")
	return nil
}

func discoverAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: torchio discover add <key> <name> [--default]")
	}
	key, name := args[0], args[1]
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

	// This asks the ush portal for confirmation on the daemon side; it blocks
	// until the user decides.
	p, err := client.AddDiscovered(control.AddDiscoveredArgs{Key: key, Name: name, MakeDefault: makeDefault})
	if err != nil {
		return err
	}
	fmt.Printf("added %s (%s) from the network\n", p.Name, orUnknown(p.MakeAndModel))
	return nil
}
