package cli

import (
	"context"
	"fmt"

	"github.com/singularityos-lab/torchio/internal/daemon"
)

// runDaemon starts torchiod, the per-user spooler. It runs until the process
// receives SIGINT/SIGTERM (the context cancelled in Dispatch), then shuts down
// cleanly.
func runDaemon(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("torchio daemon takes no arguments")
	}
	d, err := daemon.Open()
	if err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	return d.Run(ctx)
}
