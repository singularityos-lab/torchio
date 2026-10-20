// Command torchio is the client of the torchio print system. Installed as lp,
// lpr or lpstat it accepts the common forms of those commands.
package main

import (
	"fmt"
	"os"

	"github.com/singularityos-lab/torchio/internal/cli"
)

func main() {
	if err := cli.Dispatch(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "torchio:", err)
		os.Exit(1)
	}
}
