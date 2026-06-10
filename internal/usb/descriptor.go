// Package usb implements IPP-over-USB (USB Printer class, protocol 7/1/4): it
// exposes a USB printer as a local IPP endpoint so the core can speak to it
// exactly like a network printer (principle P1). It talks to the kernel through
// usbfs directly, with no libusb and no cgo. This file is the pure descriptor
// parsing: given the raw USB descriptors, find the IPP-over-USB interfaces and
// their bulk endpoints. It has no I/O, so it is fully unit testable.
package usb

import (
	"encoding/binary"
	"fmt"
)

// USB printer class identifiers. An IPP-over-USB interface is class 7 (printer),
// subclass 1 (printer), protocol 4 (IPP-USB).
const (
	classPrinter    = 0x07
	subclassPrinter = 0x01
	protocolIPPUSB  = 0x04
)

// USB descriptor type codes.
const (
	descDevice    = 0x01
	descConfig    = 0x02
	descInterface = 0x04
	descEndpoint  = 0x05
)

// DeviceInfo is the parsed device descriptor.
type DeviceInfo struct {
	VendorID  uint16
	ProductID uint16
	// Descriptor-string indices (resolved to text via control transfers later).
	IManufacturer uint8
	IProduct      uint8
	ISerial       uint8
	NumConfigs    uint8
}

// IPPInterface is one IPP-over-USB interface with its bulk endpoints. A printer
// may expose several to allow concurrent HTTP transactions; the bridge uses one
// per connection.
type IPPInterface struct {
	Number     uint8 // bInterfaceNumber
	AltSetting uint8 // bAlternateSetting
	BulkIn     uint8 // endpoint address with the 0x80 IN bit set
	BulkOut    uint8 // endpoint address (OUT)
}

// ParseDeviceDescriptor parses the 18-byte device descriptor.
func ParseDeviceDescriptor(b []byte) (DeviceInfo, error) {
	if len(b) < 18 || b[1] != descDevice {
		return DeviceInfo{}, fmt.Errorf("usb: not a device descriptor")
	}
	return DeviceInfo{
		VendorID:      binary.LittleEndian.Uint16(b[8:]),
		ProductID:     binary.LittleEndian.Uint16(b[10:]),
		IManufacturer: b[14],
		IProduct:      b[15],
		ISerial:       b[16],
		NumConfigs:    b[17],
	}, nil
}

// FindIPPInterfaces walks a configuration descriptor blob (the 9-byte config
// header followed by interface and endpoint descriptors) and returns every
// IPP-over-USB interface together with its bulk IN and OUT endpoints. Interfaces
// that are class 7/1/4 but lack both bulk endpoints are skipped.
func FindIPPInterfaces(config []byte) ([]IPPInterface, error) {
	if len(config) < 9 || config[1] != descConfig {
		return nil, fmt.Errorf("usb: not a configuration descriptor")
	}

	var (
		out      []IPPInterface
		cur      *IPPInterface
		curIsIPP bool
	)
	flush := func() {
		if cur != nil && curIsIPP && cur.BulkIn != 0 && cur.BulkOut != 0 {
			out = append(out, *cur)
		}
		cur = nil
		curIsIPP = false
	}

	i := 0
	for i+2 <= len(config) {
		length := int(config[i])
		if length < 2 || i+length > len(config) {
			break // malformed or trailing padding: stop cleanly
		}
		dType := config[i+1]
		desc := config[i : i+length]

		switch dType {
		case descInterface:
			if length < 9 {
				break
			}
			// A new interface descriptor closes the previous one.
			flush()
			class, sub, proto := desc[5], desc[6], desc[7]
			if class == classPrinter && sub == subclassPrinter && proto == protocolIPPUSB {
				cur = &IPPInterface{Number: desc[2], AltSetting: desc[3]}
				curIsIPP = true
			}
		case descEndpoint:
			if length < 7 || cur == nil || !curIsIPP {
				break
			}
			addr := desc[2]
			attrs := desc[3]
			if attrs&0x03 == 0x02 { // bulk
				if addr&0x80 != 0 {
					cur.BulkIn = addr
				} else {
					cur.BulkOut = addr
				}
			}
		}
		i += length
	}
	flush()

	return out, nil
}

// ConfigTotalLength reads wTotalLength from a config descriptor header, so a
// caller that first read only the 9-byte header knows how many more bytes to
// fetch.
func ConfigTotalLength(header []byte) (int, error) {
	if len(header) < 9 || header[1] != descConfig {
		return 0, fmt.Errorf("usb: not a configuration descriptor")
	}
	return int(binary.LittleEndian.Uint16(header[2:])), nil
}
