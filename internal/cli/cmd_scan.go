package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/singularityos-lab/torchio/internal/scan"
)

// runScan implements `torchio scan`: it pulls a scan from an eSCL (AirScan)
// scanner over the network, no vendor driver. Scanners are printers in reverse.
//
//	torchio scan --from http://scanner.local:8080/eSCL --out page.jpg [--dpi 300]
func runScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	var (
		from   = fs.String("from", "", "eSCL base URL, e.g. http://scanner.local:8080/eSCL")
		out    = fs.String("out", "", "output file (default stdout)")
		dpi    = fs.Int("dpi", 300, "scan resolution")
		color  = fs.String("color", "RGB24", "color mode: RGB24 | Grayscale8 | BlackAndWhite1")
		format = fs.String("format", "image/jpeg", "document format")
		source = fs.String("source", "Platen", "input source: Platen | Feeder")
	)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("the --from eSCL base URL is required")
	}

	scanner := scan.NewScanner(*from)
	rc, err := scanner.Scan(ctx, scan.Settings{
		Format:     *format,
		ColorMode:  *color,
		Resolution: *dpi,
		Source:     *source,
	})
	if err != nil {
		return err
	}
	defer rc.Close()

	var w io.Writer = os.Stdout
	dest := "(stdout)"
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		defer f.Close()
		w = f
		dest = *out
	}

	n, err := io.Copy(w, rc)
	if err != nil {
		return fmt.Errorf("save scan: %w", err)
	}
	if *out != "" {
		fmt.Fprintf(os.Stderr, "scanned %d bytes to %s\n", n, dest)
	}
	return nil
}
