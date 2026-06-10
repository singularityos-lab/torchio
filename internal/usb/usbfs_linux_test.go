//go:build linux

package usb

import "testing"

// TestParseNode checks the device-node parsing (device descriptor followed by a
// configuration) that OpenDevice relies on. This is the pure part of the usbfs
// path; the ioctl I/O needs real hardware (validated in the test session).
func TestParseNode(t *testing.T) {
	dev := deviceDesc(0x03f0, 0x0c17)
	cfg := configDesc(
		ifaceDesc(0, 0, classPrinter, subclassPrinter, protocolIPPUSB, 2),
		endpointDesc(0x01, 0x02),
		endpointDesc(0x81, 0x02),
	)
	node := append(append([]byte{}, dev...), cfg...)

	info, ifaces, err := parseNode(node)
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	if info.VendorID != 0x03f0 || info.ProductID != 0x0c17 {
		t.Errorf("ids = %04x:%04x", info.VendorID, info.ProductID)
	}
	if len(ifaces) != 1 || ifaces[0].BulkIn != 0x81 || ifaces[0].BulkOut != 0x01 {
		t.Errorf("ifaces = %+v", ifaces)
	}
}
