// Package doctor is the end-to-end diagnostic CUPS never had: one command that
// checks the whole print path (daemon reachable, printer reachable, TLS
// handshake, Get-Printer-Attributes) and says exactly what is wrong. It is
// built on go-foundation's health.Registry so each check is a composable
// Checker with a structured Report.
package doctor

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mirkobrombin/go-foundation/pkg/health"

	"github.com/singularityos-lab/torchio/internal/ipp"
)

// Diagnose runs all checks and returns their reports keyed by check name. The
// socket is the daemon control socket; uris are printer URIs to probe (the CLI
// gathers these from the daemon's printer list plus any explicit argument).
func Diagnose(ctx context.Context, socket string, uris []string) map[string]health.Report {
	reg := health.NewRegistry()
	reg.Register("daemon", daemonChecker{socket: socket})
	client := ipp.NewClient()
	for _, u := range uris {
		reg.Register("printer:"+u, ippChecker{client: client, uri: u})
	}
	return reg.CheckAll(ctx)
}

// daemonChecker verifies the control socket accepts a connection.
type daemonChecker struct {
	socket string
}

func (c daemonChecker) Check(ctx context.Context) health.Report {
	start := time.Now()
	conn, err := net.DialTimeout("unix", c.socket, 3*time.Second)
	if err != nil {
		return health.Report{
			Status:   health.StatusUnhealthy,
			Duration: time.Since(start),
			Details:  map[string]any{"socket": c.socket, "error": err.Error(), "hint": "is `torchio daemon` running?"},
		}
	}
	_ = conn.Close()
	return health.Report{
		Status:   health.StatusHealthy,
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

func (c ippChecker) Check(ctx context.Context) health.Report {
	start := time.Now()
	msg, err := c.client.GetPrinterAttributes(ctx, c.uri)
	if err != nil {
		return health.Report{
			Status:   health.StatusUnhealthy,
			Duration: time.Since(start),
			Details:  map[string]any{"uri": c.uri, "error": err.Error()},
		}
	}
	caps := ipp.ParseCapabilities(msg)
	status := health.StatusHealthy
	// A stopped printer is reachable but not ready: report it as degraded.
	if caps.State == 5 {
		status = health.StatusDegraded
	}
	return health.Report{
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
