package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/singularityos-lab/torchio/internal/discovery"
)

// Backend is where the driver's output (the printer's own language) goes: the
// physical device. It is an interface so the host can target a raw socket, a
// device node, or, in tests, a buffer.
type Backend interface {
	// Send writes one job's worth of bytes to the device.
	Send(ctx context.Context, r io.Reader) error
}

// BufferBackend captures output in memory, for tests.
type BufferBackend struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Send appends the bytes to the buffer.
func (b *BufferBackend) Send(_ context.Context, r io.Reader) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := io.Copy(&b.buf, r)
	return err
}

// Bytes returns a copy of everything received so far.
func (b *BufferBackend) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// FileBackend writes each job to a file or device node (e.g. /dev/usb/lp0).
type FileBackend struct {
	Path string
	// Truncate replaces the file each job instead of appending. Device nodes
	// want append semantics; regular files for debugging may want truncate.
	Truncate bool
}

// Send writes the job to the file.
func (b FileBackend) Send(_ context.Context, r io.Reader) error {
	flags := os.O_WRONLY | os.O_CREATE
	if b.Truncate {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(b.Path, flags, 0o600)
	if err != nil {
		return fmt.Errorf("host: open backend %s: %w", b.Path, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	return f.Sync()
}

// SocketBackend streams each job to a raw TCP port, the JetDirect/AppSocket
// convention (port 9100). This is the internal transport for a legacy network
// printer that speaks only its own language, never seen by the core.
type SocketBackend struct {
	Addr    string        // host:port, e.g. printer.local:9100
	Timeout time.Duration // dial timeout (default 10s)
}

// Send dials the device and writes the job.
func (b SocketBackend) Send(ctx context.Context, r io.Reader) error {
	timeout := b.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := discovery.DialContext(dialCtx, "tcp", b.Addr)
	if err != nil {
		return fmt.Errorf("host: dial backend %s: %w", b.Addr, err)
	}
	defer conn.Close()
	_, err = io.Copy(conn, r)
	return err
}
