package control

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"

	"golang.org/x/sys/unix"
)

// Server exposes a Service over a Unix domain socket using JSON-RPC. The socket
// lives in XDG_RUNTIME_DIR with 0600 permissions, and every connection is
// additionally authenticated by peer credentials (SO_PEERCRED): only the user
// who owns the daemon may call it. This is the rootless, per-user trust
// boundary of principle P4.
type Server struct {
	rpc        *rpc.Server
	ln         net.Listener
	socketPath string
	ownerUID   uint32
}

// NewServer registers svc and returns a server ready to Listen.
func NewServer(svc *Service) (*Server, error) {
	r := rpc.NewServer()
	if err := r.RegisterName("Service", svc); err != nil {
		return nil, err
	}
	return &Server{rpc: r, ownerUID: uint32(os.Getuid())}, nil
}

// Listen binds the Unix socket, removing any stale socket first.
func (s *Server) Listen(socketPath string) error {
	// A leftover socket from a crashed daemon would block bind; clear it.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("control: clear stale socket: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("control: listen: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("control: chmod socket: %w", err)
	}
	s.ln = ln
	s.socketPath = socketPath
	return nil
}

// Serve accepts connections until the context is cancelled or the listener is
// closed. Each connection is handled on its own goroutine.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.ln.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil // clean shutdown
			default:
				return err
			}
		}
		if !s.peerAllowed(conn) {
			conn.Close()
			continue
		}
		go s.rpc.ServeCodec(jsonrpc.NewServerCodec(conn))
	}
}

// Close stops the listener and removes the socket file.
func (s *Server) Close() error {
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	if s.socketPath != "" {
		_ = os.Remove(s.socketPath)
	}
	return err
}

// peerAllowed checks SO_PEERCRED: the connecting process must belong to the
// same user that owns the daemon.
func (s *Server) peerAllowed(conn net.Conn) bool {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return false
	}
	if credErr != nil {
		return false
	}
	return cred.Uid == s.ownerUID
}
