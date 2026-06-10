//go:build linux

package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/singularityos-lab/torchio/internal/usb"
)

// runUsb implements `torchio usb`, the IPP-over-USB bridge.
//
//	torchio usb ls               list IPP-over-USB printers found on the bus
//	torchio usb bridge           expose each as ipp://127.0.0.1:PORT and run
//	    [--quirks <file>]        optional quirks database
func runUsb(ctx context.Context, args []string) error {
	action := "ls"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	switch action {
	case "ls":
		return usbList()
	case "bridge":
		return usbBridge(ctx, args)
	default:
		return fmt.Errorf("unknown usb action %q (ls, bridge)", action)
	}
}

func usbList() error {
	found, err := usb.Enumerate()
	if err != nil {
		return fmt.Errorf("scan usb: %w", err)
	}
	if len(found) == 0 {
		return fmt.Errorf("no IPP-over-USB printers found (check permissions on /dev/bus/usb)")
	}
	for _, d := range found {
		fmt.Printf("%s  %04x:%04x  %d interface(s)\n",
			d.Path, d.Info.VendorID, d.Info.ProductID, len(d.Interfaces))
	}
	return nil
}

func usbBridge(ctx context.Context, args []string) error {
	quirksFile := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--quirks" && i+1 < len(args) {
			quirksFile = args[i+1]
			i++
		} else {
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	db, err := loadQuirks(quirksFile)
	if err != nil {
		return err
	}

	found, err := usb.Enumerate()
	if err != nil {
		return fmt.Errorf("scan usb: %w", err)
	}
	if len(found) == 0 {
		return fmt.Errorf("no IPP-over-USB printers found")
	}

	var wg sync.WaitGroup
	started := 0
	for _, d := range found {
		dev, err := usb.OpenDevice(d.Path, db.Lookup(d.Info.VendorID, d.Info.ProductID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", d.Path, err)
			continue
		}
		bridge := usb.NewBridge(dev)
		if err := bridge.Listen("127.0.0.1:0"); err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", d.Path, err)
			dev.Close()
			continue
		}
		fmt.Printf("%04x:%04x -> %s\n", d.Info.VendorID, d.Info.ProductID, bridge.URI("/ipp/print"))
		started++
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer dev.Close()
			_ = bridge.Serve(ctx)
		}()
	}
	if started == 0 {
		return fmt.Errorf("no USB printer could be opened (need write access to /dev/bus/usb)")
	}
	fmt.Println("bridging; add a printer with `torchio printer add`, Ctrl-C to stop")
	wg.Wait()
	return nil
}

// loadQuirks loads a quirks DB from a file, or returns an empty DB if path is "".
func loadQuirks(path string) (*usb.QuirksDB, error) {
	if path == "" {
		return usb.ParseQuirks(strings.NewReader(""))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open quirks: %w", err)
	}
	defer f.Close()
	return usb.ParseQuirks(f)
}
