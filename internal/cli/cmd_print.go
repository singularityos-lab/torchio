package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/singularityos-lab/torchio/internal/control"
	"github.com/singularityos-lab/torchio/internal/detect"
	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
)

// runPrint implements `torchio print`. In M0 there is no daemon and no
// transform engine: the document is sniffed, validated against the printer and
// sent verbatim. If the printer does not accept the document's format directly,
// M0 refuses rather than rasterizing (the transform engine arrives in M3).
//
//	torchio print --to ipps://printer.local/ipp/print [--copies N] [--duplex]
//	              [--media iso_a4_210x297mm] [--job-name NAME] [file]
//
// With no file argument the document is read from stdin, so `lpr` pipelines
// keep working.
func runPrint(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("print", flag.ContinueOnError)
	var (
		to      = fs.String("to", "", "printer URI (ipp:// or ipps://) for a direct send")
		queue   = fs.String("queue", "", "configured printer name: route through the spooler (transforms, retries)")
		copies  = fs.Int("copies", 0, "number of copies (0 = printer default)")
		duplex  = fs.Bool("duplex", false, "print two-sided (long edge)")
		media   = fs.String("media", "", "media size, e.g. iso_a4_210x297mm")
		jobName = fs.String("job-name", "", "job name (defaults to the file name)")
		user    = fs.String("user", "", "requesting user name")
		force   = fs.Bool("force", false, "send even if the format is not advertised as supported")
	)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" && *queue == "" {
		return fmt.Errorf("specify --to <uri> for a direct send or --queue <printer> to go through the spooler")
	}

	// --queue routes the job through the daemon's spooler, which runs the
	// transform pipeline (e.g. rasterize a PDF for a raster-only printer) and
	// retries; --to is the direct, daemon-less M0 path.
	if *queue != "" {
		return printViaQueue(*queue, fs, *jobName, *copies, *duplex, *media)
	}

	// Resolve the document source: a file argument or stdin.
	var (
		src      io.Reader
		srcName  string
		fileName string
	)
	if fs.NArg() > 0 {
		fileName = fs.Arg(0)
		f, err := os.Open(fileName)
		if err != nil {
			return fmt.Errorf("open document: %w", err)
		}
		defer f.Close()
		src = f
		srcName = fileName
	} else {
		src = os.Stdin
		srcName = "(stdin)"
	}

	// Sniff the real format; the detector returns a reader that replays the
	// full stream, so nothing is lost for the actual send.
	format, body, err := detect.Format(src)
	if err != nil {
		return fmt.Errorf("detect format of %s: %w", srcName, err)
	}
	if format == model.MIMEOctet {
		return fmt.Errorf("could not determine the format of %s (refusing to print unknown data)", srcName)
	}

	opts := []ipp.Option{}
	if *user != "" {
		opts = append(opts, ipp.WithUserName(*user))
	}
	client := ipp.NewClient(opts...)

	// Confirm the printer takes this format directly (M0 fast path only).
	caps, err := probeCapabilities(ctx, client, *to)
	if err != nil {
		return err
	}
	if !caps.AcceptsFormat(format) && !*force {
		return fmt.Errorf(
			"printer does not accept %s directly (supports: %s); transform engine arrives in M3, or retry with --force",
			format, joinFormats(caps.DocumentFormats))
	}

	name := *jobName
	if name == "" {
		name = displayName(fileName)
	}

	req := ipp.PrintRequest{
		PrinterURI:     *to,
		JobName:        name,
		DocumentFormat: format.String(),
		Copies:         *copies,
		Media:          *media,
		Document:       body,
	}
	if *duplex {
		req.Sides = "two-sided-long-edge"
	}

	// Validate-Job first: cheap, catches an unacceptable ticket before we
	// stream the whole document.
	if err := client.ValidateJob(ctx, req); err != nil {
		return fmt.Errorf("printer rejected the job ticket: %w", err)
	}

	res, err := client.PrintJob(ctx, req)
	if err != nil {
		return fmt.Errorf("print: %w", err)
	}

	fmt.Printf("submitted %s as %s, job-id %d\n", srcName, format, res.JobID)
	return nil
}

// printViaQueue submits the document to the daemon's spooler for the named
// printer. The spooler sniffs, transforms (if needed) and sends it, with
// retries. The whole document is read into memory and sent inline.
func printViaQueue(printer string, fs *flag.FlagSet, jobName string, copies int, duplex bool, media string) error {
	var (
		data    []byte
		err     error
		srcName string
	)
	if fs.NArg() > 0 {
		srcName = fs.Arg(0)
		data, err = os.ReadFile(srcName)
		if err != nil {
			return fmt.Errorf("open document: %w", err)
		}
	} else {
		srcName = "(stdin)"
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
	}

	name := jobName
	if name == "" {
		name = displayName(fs.Arg(0))
	}
	ticket := control.TicketDTO{Copies: copies, Media: media}
	if duplex {
		ticket.Sides = "two-sided-long-edge"
	}

	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()

	jobID, err := client.Submit(control.SubmitArgs{Printer: printer, Name: name, Ticket: ticket, Doc: data})
	if err != nil {
		return err
	}
	fmt.Printf("queued %s on %s, job %s\n", srcName, printer, jobID)
	return nil
}

// probeCapabilities fetches and parses the printer's capabilities.
func probeCapabilities(ctx context.Context, client *ipp.Client, uri string) (model.Capabilities, error) {
	msg, err := client.GetPrinterAttributes(ctx, uri)
	if err != nil {
		return model.Capabilities{}, fmt.Errorf("query printer %s: %w", uri, err)
	}
	return ipp.ParseCapabilities(msg), nil
}

// displayName derives a job name from a file path, falling back to a generic
// label for stdin.
func displayName(path string) string {
	if path == "" {
		return "torchio job"
	}
	return filepath.Base(path)
}

// joinFormats renders a format list for an error message.
func joinFormats(fs []model.MIME) string {
	if len(fs) == 0 {
		return "none advertised"
	}
	out := ""
	for i, f := range fs {
		if i > 0 {
			out += ", "
		}
		out += f.String()
	}
	return out
}
