package share

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"

	"github.com/singularityos-lab/torchio/internal/host"
	"github.com/singularityos-lab/torchio/internal/model"
)

// Server re-exposes one local printer to the network over IPPS. It reuses the
// printer-side IPP server from the host package (a hosted queue and a shared
// queue are both "answer IPP, accept jobs"), but listens over TLS and forwards
// each job to the spooler instead of a local driver.
type Server struct {
	name   string
	server *host.Server
	http   *http.Server
	ln     net.Listener
}

// New builds a share server for a printer with the given capabilities. submit
// forwards a received document to the spooler (the CLI wires it to the control
// client).
func New(name string, caps model.Capabilities, primary model.MIME, submit host.SubmitFunc) *Server {
	s := &Server{name: name, server: host.NewServer(name, caps, primary, submit)}
	s.http = &http.Server{Handler: s.server}
	return s
}

// ListenTLS binds an IPPS (TLS) listener. Plain IPP is never offered: sharing
// is encrypted only.
func (s *Server) ListenTLS(addr string, cert tls.Certificate) error {
	raw, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("share: listen: %w", err)
	}
	s.ln = tls.NewListener(raw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	return nil
}

// Addr returns the bound host:port.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// URI returns the ipps:// URI other machines use to reach the shared printer.
func (s *Server) URI() string {
	return "ipps://" + s.Addr() + "/ipp/print"
}

// Serve answers IPPS requests until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.http.Close()
	}()
	if err := s.http.Serve(s.ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Close stops the server.
func (s *Server) Close() error { return s.http.Close() }
