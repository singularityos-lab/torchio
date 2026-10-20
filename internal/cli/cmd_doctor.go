package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/singularityos-lab/torchio/internal/doctor"
	"github.com/singularityos-lab/torchio/internal/paths"
)

// runDoctor implements `torchio doctor [uri]`. With no argument it checks the
// daemon and every configured printer. With a URI it checks just that printer
// (no daemon required), which is handy before adding it.
func runDoctor(ctx context.Context, args []string) error {
	socket, err := paths.SocketPath()
	if err != nil {
		return err
	}

	var uris []string
	if len(args) == 1 {
		uris = append(uris, args[0])
	} else if len(args) > 1 {
		return fmt.Errorf("usage: torchio doctor [printer-uri]")
	} else {
		// No explicit URI: ask the daemon for the configured printers. If the
		// daemon is down, that itself is the first thing doctor reports.
		if client, derr := dialDaemon(); derr == nil {
			if printers, lerr := client.ListPrinters(); lerr == nil {
				for _, p := range printers {
					uris = append(uris, p.URI)
				}
			}
			client.Close()
		}
	}

	reports := doctor.Diagnose(ctx, socket, uris)

	// Stable output order: daemon first, then printers sorted by name.
	names := make([]string, 0, len(reports))
	for n := range reports {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "daemon" {
			return true
		}
		if names[j] == "daemon" {
			return false
		}
		return names[i] < names[j]
	})

	allOK := true
	for _, n := range names {
		r := reports[n]
		if r.Status != doctor.StatusHealthy {
			allOK = false
		}
		fmt.Printf("%s  %s\n", statusMark(r.Status), n)
		for _, line := range detailLines(r.Details) {
			fmt.Printf("      %s\n", line)
		}
	}
	if !allOK {
		return fmt.Errorf("some checks did not pass")
	}
	return nil
}

// statusMark renders a health status as a short ASCII marker.
func statusMark(s doctor.Status) string {
	switch s {
	case doctor.StatusHealthy:
		return "[ ok ]"
	case doctor.StatusDegraded:
		return "[warn]"
	default:
		return "[FAIL]"
	}
}

// detailLines renders a report's details as stable "key: value" lines.
func detailLines(d map[string]any) []string {
	if len(d) == 0 {
		return nil
	}
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s: %v", k, d[k]))
	}
	return out
}
