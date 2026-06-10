// Command torchio is the single static multi-call binary for the Torchio print
// system (principle P5). Depending on argv[0] or the first argument it acts as
// the spooler daemon, a print client, a queue manager, a diagnostics tool or a
// legacy-driver host. The lp/lpr/lpstat compatibility shims are symlinks to
// this binary that dispatch on the program name.
package main

import (
	"fmt"
	"os"

	"github.com/mirkobrombin/go-foundation/pkg/errutil"

	"github.com/singularityos-lab/torchio/internal/cli"
)

func main() {
	// Styled panic traces from the foundation lib (P5 single binary, one entry).
	defer errutil.Auto()

	if err := cli.Dispatch(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "torchio:", err)
		os.Exit(1)
	}
}
