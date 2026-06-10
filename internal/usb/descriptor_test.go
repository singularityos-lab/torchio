package usb

import (
	"encoding/binary"
	"testing"
)

// builder helpers for crafting USB descriptors.

func deviceDesc(vid, pid uint16) []byte {
	b := make([]byte, 18)
	b[0] = 18
	b[1] = descDevice
	binary.LittleEndian.PutUint16(b[8:], vid)
	binary.LittleEndian.PutUint16(b[10:], pid)
	b[14], b[15], b[16] = 1, 2, 3 // string indices
	b[17] = 1                     // num configs
	return b
}

func ifaceDesc(num, alt, class, sub, proto, numEnd byte) []byte {
	return []byte{9, descInterface, num, alt, numEnd, class, sub, proto, 0}
}

func endpointDesc(addr, attrs byte) []byte {
	return []byte{7, descEndpoint, addr, attrs, 0x00, 0x02, 0} // wMaxPacketSize 512
}

func configDesc(body ...[]byte) []byte {
	var inner []byte
	for _, d := range body {
		inner = append(inner, d...)
	}
	total := 9 + len(inner)
	hdr := make([]byte, 9)
	hdr[0] = 9
	hdr[1] = descConfig
	binary.LittleEndian.PutUint16(hdr[2:], uint16(total))
	hdr[4] = 1 // num interfaces
	return append(hdr, inner...)
}

func TestParseDeviceDescriptor(t *testing.T) {
	d, err := ParseDeviceDescriptor(deviceDesc(0x03f0, 0x1234))
	if err != nil {
		t.Fatal(err)
	}
	if d.VendorID != 0x03f0 || d.ProductID != 0x1234 {
		t.Errorf("ids = %04x:%04x", d.VendorID, d.ProductID)
	}
	if d.IManufacturer != 1 || d.IProduct != 2 || d.ISerial != 3 {
		t.Errorf("string indices = %+v", d)
	}
}

func TestParseDeviceDescriptorRejectsJunk(t *testing.T) {
	if _, err := ParseDeviceDescriptor([]byte{1, 2, 3}); err == nil {
		t.Error("expected error for short descriptor")
	}
}

func TestFindIPPInterfaces(t *testing.T) {
	// A printer with: interface 0 = vendor-specific (ignored), interface 1 and
	// 2 = IPP-over-USB each with a bulk OUT (0x01/0x02) and bulk IN (0x81/0x82).
	cfg := configDesc(
		ifaceDesc(0, 0, 0xff, 0, 0, 2), // vendor specific
		endpointDesc(0x03, 0x02),
		endpointDesc(0x83, 0x02),
		ifaceDesc(1, 0, classPrinter, subclassPrinter, protocolIPPUSB, 2),
		endpointDesc(0x01, 0x02), // bulk OUT
		endpointDesc(0x81, 0x02), // bulk IN
		ifaceDesc(2, 0, classPrinter, subclassPrinter, protocolIPPUSB, 2),
		endpointDesc(0x02, 0x02),
		endpointDesc(0x82, 0x02),
	)

	ifs, err := FindIPPInterfaces(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ifs) != 2 {
		t.Fatalf("found %d IPP interfaces, want 2: %+v", len(ifs), ifs)
	}
	if ifs[0].Number != 1 || ifs[0].BulkOut != 0x01 || ifs[0].BulkIn != 0x81 {
		t.Errorf("interface 1 = %+v", ifs[0])
	}
	if ifs[1].Number != 2 || ifs[1].BulkOut != 0x02 || ifs[1].BulkIn != 0x82 {
		t.Errorf("interface 2 = %+v", ifs[1])
	}
}

func TestFindIPPInterfacesSkipsIncomplete(t *testing.T) {
	// An IPP interface that only has a bulk IN (no OUT) is unusable and skipped.
	cfg := configDesc(
		ifaceDesc(0, 0, classPrinter, subclassPrinter, protocolIPPUSB, 1),
		endpointDesc(0x81, 0x02), // only IN
	)
	ifs, err := FindIPPInterfaces(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ifs) != 0 {
		t.Errorf("expected 0 usable interfaces, got %+v", ifs)
	}
}

func TestFindIPPInterfacesIgnoresNonBulk(t *testing.T) {
	// An interrupt endpoint (attrs 0x03) must not be taken as a bulk endpoint.
	cfg := configDesc(
		ifaceDesc(0, 0, classPrinter, subclassPrinter, protocolIPPUSB, 3),
		endpointDesc(0x01, 0x02), // bulk OUT
		endpointDesc(0x81, 0x02), // bulk IN
		endpointDesc(0x82, 0x03), // interrupt IN, ignored
	)
	ifs, err := FindIPPInterfaces(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ifs) != 1 || ifs[0].BulkIn != 0x81 {
		t.Errorf("interfaces = %+v", ifs)
	}
}

func TestConfigTotalLength(t *testing.T) {
	cfg := configDesc(ifaceDesc(0, 0, classPrinter, subclassPrinter, protocolIPPUSB, 0))
	n, err := ConfigTotalLength(cfg[:9])
	if err != nil {
		t.Fatal(err)
	}
	if n != len(cfg) {
		t.Errorf("total length = %d, want %d", n, len(cfg))
	}
}
