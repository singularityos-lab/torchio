// Command torchiod is the per-user print spooler. It serves the torchio control
// socket, the CUPS-compatible socket and the XDG print portal backend.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/singularityos-lab/torchio/internal/daemon"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("torchiod: ")
	if len(os.Args) > 1 {
		log.Fatalf("unexpected argument %q", os.Args[1])
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := daemon.Open()
	if err != nil {
		log.Fatal(err)
	}
	if err := d.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
