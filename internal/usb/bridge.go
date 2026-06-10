package usb

import (
	"context"
	"fmt"
	"io"
	"net"
)

// Conn is one IPP-over-USB interface seen as a byte stream: Read pulls from the
// printer's bulk-IN endpoint, Write pushes to bulk-OUT. IPP-over-USB tunnels
// raw HTTP over these endpoints, so the bridge does not need to understand HTTP;
// it shuttles bytes. The real implementation is the usbfs transport; tests use
// an in-memory pipe wired to a fake printer.
type Conn = io.ReadWriteCloser

// Transport hands out connections to the printer. Each Dial yields a usable
// IPP-over-USB interface; a printer that exposes several can serve several
// connections at once.
type Transport interface {
	Dial() (Conn, error)
}

// Bridge exposes a USB printer as a local IPP endpoint. It listens on a
// loopback TCP port and tunnels each accepted connection to a USB interface, so
// the rest of Torchio talks to ipp://127.0.0.1:PORT exactly as to a network
// printer (principle P1).
type Bridge struct {
	transport Transport
	ln        net.Listener
}

// NewBridge builds a bridge over the given transport.
func NewBridge(t Transport) *Bridge {
	return &Bridge{transport: t}
}

// Listen binds a loopback TCP socket. Use "127.0.0.1:0" to get an ephemeral
// port, then read it back with Addr.
func (b *Bridge) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("usb: bridge listen: %w", err)
	}
	b.ln = ln
	return nil
}

// Addr returns the bound host:port.
func (b *Bridge) Addr() string {
	if b.ln == nil {
		return ""
	}
	return b.ln.Addr().String()
}

// URI returns the ipp:// URI clients use to reach this printer. path is the
// printer's resource path, e.g. "/ipp/print".
func (b *Bridge) URI(path string) string {
	if path == "" {
		path = "/ipp/print"
	}
	return "ipp://" + b.Addr() + path
}

// Serve accepts connections until ctx is cancelled or the listener closes.
func (b *Bridge) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = b.ln.Close()
	}()
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go b.handle(conn)
	}
}

// Close stops the listener.
func (b *Bridge) Close() error {
	if b.ln != nil {
		return b.ln.Close()
	}
	return nil
}

// handle tunnels one TCP connection to a freshly dialed USB interface. Bytes
// flow in both directions until either side closes; closing one unblocks the
// other.
func (b *Bridge) handle(tcp net.Conn) {
	defer tcp.Close()

	usb, err := b.transport.Dial()
	if err != nil {
		return
	}
	defer usb.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(usb, tcp); done <- struct{}{} }() // client -> printer
	go func() { _, _ = io.Copy(tcp, usb); done <- struct{}{} }() // printer -> client

	// When either direction ends, return: the defers close both endpoints,
	// which unblocks the other io.Copy.
	<-done
}
