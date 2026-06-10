// Command torchio-fakeprinter runs a real, inspectable IPP printer for testing
// Torchio end to end. It advertises whatever formats you configure (so you can
// force the PDF fast path or force rasterization), saves every received
// document to disk, and logs the declared vs actually-detected format, so you
// can verify exactly what reached the "printer".
//
//	torchio-fakeprinter --listen 127.0.0.1:0 --formats application/pdf --save /tmp/fake
//	torchio-fakeprinter --formats image/pwg-raster --save /tmp/raster   # raster-only
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/singularityos-lab/torchio/internal/fakeprinter"
	"github.com/singularityos-lab/torchio/internal/model"
)

func main() {
	var (
		listen  = flag.String("listen", "127.0.0.1:0", "address to listen on")
		name    = flag.String("name", "Torchio Fake Printer", "printer make and model")
		formats = flag.String("formats", "application/pdf,image/pwg-raster,image/urf", "comma-separated document-format-supported")
		media   = flag.String("media", "iso_a4_210x297mm,na_letter_8.5x11in", "comma-separated media-supported")
		sides   = flag.String("sides", "one-sided,two-sided-long-edge", "comma-separated sides-supported")
		resos   = flag.String("resolutions", "300dpi,600dpi", "comma-separated resolutions")
		colors  = flag.String("colors", "monochrome,color", "comma-separated color modes")
		save    = flag.String("save", "", "directory to save received documents (required to inspect output)")
	)
	flag.Parse()

	cfg := fakeprinter.Config{
		Name:        *name,
		Formats:     mimes(*formats),
		Media:       split(*media),
		Sides:       split(*sides),
		Resolutions: split(*resos),
		Colors:      split(*colors),
		SaveDir:     *save,
		Logf:        log.Printf,
	}
	fp := fakeprinter.New(cfg)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	fmt.Printf("fake printer %q at ipp://%s/ipp/print\n", *name, ln.Addr().String())
	fmt.Printf("advertising formats: %s\n", *formats)
	if *save != "" {
		fmt.Printf("saving received documents to: %s\n", *save)
	}

	srv := &http.Server{Handler: fp}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func mimes(s string) []model.MIME {
	var out []model.MIME
	for _, p := range split(s) {
		out = append(out, model.MIME(p))
	}
	return out
}
