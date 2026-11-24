// Command torchiod is the per-user print spooler. It serves the torchio control
// socket, the CUPS-compatible socket and the XDG print portal backend.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mirkobrombin/atomloops/sealedlog"

	"github.com/singularityos-lab/torchio/internal/daemon"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("torchiod: ")
	if len(os.Args) > 1 {
		log.Fatalf("unexpected argument %q", os.Args[1])
	}

	sealed, _ := sealedlog.Open("torchio")
	sealed.Log("start")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := daemon.Open()
	if err != nil {
		sealed.Log("fail", 1)
		log.Fatal(err)
	}
	d.OnReady = func() { sealed.Log("ready") }
	if err := d.Run(ctx); err != nil {
		sealed.Log("fail", 2)
		log.Fatal(err)
	}
}
