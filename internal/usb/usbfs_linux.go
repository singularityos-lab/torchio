//go:build linux

package usb

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// This is the only file in Torchio that touches hardware. It speaks to the
// kernel through usbfs (/dev/bus/usb) with raw ioctls, no libusb and no cgo.
// It cannot be unit tested without a real USB printer, so the testable logic
// (descriptor parsing, the bridge, quirks) lives behind the Transport interface
// and this file just satisfies it. The ioctl request codes are the standard
// usbdevice_fs.h values for 64-bit Linux.
const (
	usbdevfsClaimInterface   = 0x8004550f // _IOR('U', 15, unsigned int)
	usbdevfsReleaseInterface = 0x80045510 // _IOR('U', 16, unsigned int)
	usbdevfsBulk             = 0xc0185502 // _IOWR('U', 2, struct usbdevfs_bulktransfer)
	usbdevfsIoctl            = 0xc0105512 // _IOWR('U', 18, struct usbdevfs_ioctl)
	usbdevfsDisconnect       = 0x5516     // _IO('U', 22): detach kernel driver
)

// bulkTransfer mirrors struct usbdevfs_bulktransfer. The pointer field is
// 8-aligned on 64-bit, so an explicit pad keeps the layout exact.
type bulkTransfer struct {
	ep      uint32
	length  uint32
	timeout uint32 // milliseconds; 0 means wait indefinitely
	_       uint32 // padding to 8-align data
	data    uint64 // pointer to the buffer
}

// usbfsIoctl mirrors struct usbdevfs_ioctl, used to send a nested ioctl (here,
// to detach the kernel printer driver from an interface).
type usbfsIoctl struct {
	ifno      int32
	ioctlCode int32
	data      uint64
}

// Device is the usbfs implementation of Transport. It claims the printer's
// IPP-over-USB interfaces and leases them to the bridge one connection at a
// time.
type Device struct {
	f      *os.File
	info   DeviceInfo
	ifaces []IPPInterface
	quirks Quirks

	mu   sync.Mutex
	free []int // indices into ifaces that are not currently leased
}

// OpenDevice opens the usbfs node at path (e.g. /dev/bus/usb/003/004), parses
// its descriptors, detaches any kernel driver and claims the IPP-over-USB
// interfaces (capped by the quirks max-interfaces).
func OpenDevice(path string, quirks Quirks) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("usb: open %s: %w", path, err)
	}

	raw := make([]byte, 1<<16)
	n, err := f.Read(raw)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("usb: read descriptors: %w", err)
	}
	raw = raw[:n]

	info, ifaces, err := parseNode(raw)
	if err != nil {
		f.Close()
		return nil, err
	}
	if len(ifaces) == 0 {
		f.Close()
		return nil, fmt.Errorf("usb: %s is not an IPP-over-USB printer", path)
	}
	if quirks.MaxInterfaces > 0 && len(ifaces) > quirks.MaxInterfaces {
		ifaces = ifaces[:quirks.MaxInterfaces]
	}

	d := &Device{f: f, info: info, ifaces: ifaces, quirks: quirks}
	for i, iface := range ifaces {
		d.detachKernelDriver(iface.Number)
		if err := d.claim(iface.Number); err != nil {
			d.Close()
			return nil, fmt.Errorf("usb: claim interface %d: %w", iface.Number, err)
		}
		d.free = append(d.free, i)
	}
	return d, nil
}

// Info returns the parsed device descriptor.
func (d *Device) Info() DeviceInfo { return d.info }

// Dial leases a free IPP-over-USB interface as a Conn. It errors if all
// interfaces are in use.
func (d *Device) Dial() (Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.free) == 0 {
		return nil, fmt.Errorf("usb: all %d interfaces busy", len(d.ifaces))
	}
	idx := d.free[len(d.free)-1]
	d.free = d.free[:len(d.free)-1]
	return &usbConn{dev: d, idx: idx, iface: d.ifaces[idx]}, nil
}

// release returns a leased interface to the pool.
func (d *Device) release(idx int) {
	d.mu.Lock()
	d.free = append(d.free, idx)
	d.mu.Unlock()
}

// Close releases all interfaces and closes the device node.
func (d *Device) Close() error {
	for _, iface := range d.ifaces {
		num := uint32(iface.Number)
		_ = d.ioctl(usbdevfsReleaseInterface, uintptr(unsafe.Pointer(&num)))
	}
	return d.f.Close()
}

func (d *Device) claim(ifnum uint8) error {
	n := uint32(ifnum)
	return d.ioctl(usbdevfsClaimInterface, uintptr(unsafe.Pointer(&n)))
}

// detachKernelDriver asks the kernel to release the interface (usblp usually
// grabs printers). Errors are ignored: the driver may simply not be bound.
func (d *Device) detachKernelDriver(ifnum uint8) {
	cmd := usbfsIoctl{ifno: int32(ifnum), ioctlCode: usbdevfsDisconnect}
	_ = d.ioctl(usbdevfsIoctl, uintptr(unsafe.Pointer(&cmd)))
}

func (d *Device) ioctl(req, arg uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, d.f.Fd(), req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

// bulk performs one synchronous bulk transfer on an endpoint. For IN endpoints
// it reads into buf and returns the count; for OUT it writes buf.
func (d *Device) bulk(endpoint uint8, buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	bt := bulkTransfer{
		ep:      uint32(endpoint),
		length:  uint32(len(buf)),
		timeout: 0, // block until the printer is ready or the fd is closed
		data:    uint64(uintptr(unsafe.Pointer(&buf[0]))),
	}
	r, _, errno := unix.Syscall(unix.SYS_IOCTL, d.f.Fd(), usbdevfsBulk, uintptr(unsafe.Pointer(&bt)))
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}

// usbConn is one leased interface presented as an io.ReadWriteCloser. Read pulls
// from the bulk-IN endpoint, Write pushes to bulk-OUT.
type usbConn struct {
	dev    *Device
	idx    int
	iface  IPPInterface
	closed bool
}

func (c *usbConn) Read(p []byte) (int, error) {
	return c.dev.bulk(c.iface.BulkIn, p)
}

func (c *usbConn) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		n, err := c.dev.bulk(c.iface.BulkOut, p[written:])
		if err != nil {
			return written, err
		}
		if n == 0 {
			break
		}
		written += n
	}
	return written, nil
}

func (c *usbConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	c.dev.release(c.idx)
	return nil
}

// parseNode parses the device descriptor and the first configuration from a
// usbfs node read, returning the device info and its IPP-over-USB interfaces.
func parseNode(raw []byte) (DeviceInfo, []IPPInterface, error) {
	info, err := ParseDeviceDescriptor(raw)
	if err != nil {
		return DeviceInfo{}, nil, err
	}
	if len(raw) < 18+9 {
		return info, nil, nil // no configuration present
	}
	config := raw[18:]
	if total, err := ConfigTotalLength(config[:9]); err == nil && total <= len(config) {
		config = config[:total]
	}
	ifaces, err := FindIPPInterfaces(config)
	if err != nil {
		return info, nil, err
	}
	return info, ifaces, nil
}
