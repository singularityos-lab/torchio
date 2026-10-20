// Package doctor is the end-to-end diagnostic CUPS never had: one command that
// checks the whole print path (daemon reachable, printer reachable, TLS
// handshake, Get-Printer-Attributes) and says exactly what is wrong.
package doctor

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/singularityos-lab/torchio/internal/ipp"
)

// Status is the outcome of one check.
type Status int

const (
	StatusHealthy Status = iota
	// StatusDegraded means reachable but not ready, such as a stopped printer.
	StatusDegraded
	StatusUnhealthy
)

// Report is the result of one check.
type Report struct {
	Status   Status
	Duration time.Duration
	Details  map[string]any
}

type checker interface {
	Check(ctx context.Context) Report
}

// Diagnose runs all checks and returns their reports keyed by check name. The
// socket is the daemon control socket; uris are printer URIs to probe (the CLI
// gathers these from the daemon's printer list plus any explicit argument).
func Diagnose(ctx context.Context, socket string, uris []string) map[string]Report {
	checks := map[string]checker{"daemon": daemonChecker{socket: socket}}
	client := ipp.NewClient()
	for _, u := range uris {
		checks["printer:"+u] = ippChecker{client: client, uri: u}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := make(map[string]Report, len(checks))
	for name, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := c.Check(ctx)
			mu.Lock()
			out[name] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// daemonChecker verifies the control socket accepts a connection.
type daemonChecker struct {
	socket string
}

func (c daemonChecker) Check(ctx context.Context) Report {
	start := time.Now()
	conn, err := net.DialTimeout("unix", c.socket, 3*time.Second)
	if err != nil {
		return Report{
			Status:   StatusUnhealthy,
			Duration: time.Since(start),
			Details:  map[string]any{"socket": c.socket, "error": err.Error(), "hint": "is torchiod running?"},
		}
	}
	_ = conn.Close()
	return Report{
		Status:   StatusHealthy,
		Duration: time.Since(start),
		Details:  map[string]any{"socket": c.socket},
	}
}

// ippChecker performs a live Get-Printer-Attributes against a printer URI. This
// single call exercises DNS, TCP, the TLS handshake (for ipps), and IPP itself.
type ippChecker struct {
	client *ipp.Client
	uri    string
}

func (c ippChecker) Check(ctx context.Context) Report {
	start := time.Now()
	msg, err := c.client.GetPrinterAttributes(ctx, c.uri)
	if err != nil {
		return Report{
			Status:   StatusUnhealthy,
			Duration: time.Since(start),
			Details:  map[string]any{"uri": c.uri, "error": err.Error()},
		}
	}
	caps := ipp.ParseCapabilities(msg)
	status := StatusHealthy
	// A stopped printer is reachable but not ready: report it as degraded.
	if caps.State == 5 {
		status = StatusDegraded
	}
	return Report{
		Status:   status,
		Duration: time.Since(start),
		Details: map[string]any{
			"uri":     c.uri,
			"model":   caps.MakeAndModel,
			"state":   stateName(caps.State),
			"formats": caps.DocumentFormats,
		},
	}
}

func stateName(s int) string {
	switch s {
	case 3:
		return "idle"
	case 4:
		return "processing"
	case 5:
		return "stopped"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}
