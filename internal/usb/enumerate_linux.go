//go:build linux

package usb

import (
	"os"
	"path/filepath"
)

// usbfsRoot is where the kernel exposes USB device nodes.
const usbfsRoot = "/dev/bus/usb"

// Found is a discovered IPP-over-USB printer (before any interface is claimed).
type Found struct {
	Path       string // /dev/bus/usb/BBB/DDD
	Info       DeviceInfo
	Interfaces []IPPInterface
}

// Enumerate scans usbfs for IPP-over-USB printers. It reads each device node's
// descriptors read-only (no claim, no write), so it is safe to call without
// disturbing a printer the kernel may be using. Nodes it cannot read are
// skipped, not fatal.
func Enumerate() ([]Found, error) {
	buses, err := os.ReadDir(usbfsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no usbfs (e.g. container): no USB printers
		}
		return nil, err
	}

	var found []Found
	for _, bus := range buses {
		busDir := filepath.Join(usbfsRoot, bus.Name())
		devs, err := os.ReadDir(busDir)
		if err != nil {
			continue
		}
		for _, dev := range devs {
			path := filepath.Join(busDir, dev.Name())
			f, err := os.OpenFile(path, os.O_RDONLY, 0)
			if err != nil {
				continue
			}
			raw := make([]byte, 1<<16)
			n, _ := f.Read(raw)
			f.Close()
			if n <= 0 {
				continue
			}
			info, ifaces, err := parseNode(raw[:n])
			if err != nil || len(ifaces) == 0 {
				continue
			}
			found = append(found, Found{Path: path, Info: info, Interfaces: ifaces})
		}
	}
	return found, nil
}
