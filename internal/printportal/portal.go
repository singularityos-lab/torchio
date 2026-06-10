// Package printportal implements the XDG print portal backend
// (org.freedesktop.impl.portal.Print) so sandboxed GTK apps print through
// Torchio without knowing anything about it. xdg-desktop-portal routes an app's
// Print request to this backend; the backend turns the GTK print settings into
// a Torchio job ticket and submits the document (received as a file descriptor)
// to the spooler.
//
// The D-Bus glue lives in dbus.go; this file is the pure logic (settings
// conversion, the prepare/print handlers, the token store), which is unit
// testable without a session bus.
package printportal

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/singularityos-lab/torchio/internal/model"
)

// Response codes returned to xdg-desktop-portal.
const (
	responseSuccess   uint32 = 0
	responseCancelled uint32 = 1
	responseEnded     uint32 = 2
)

// Jobs is the slice of the spooler the portal needs. The daemon implements it
// over the store and queue engine; tests use a fake.
type Jobs interface {
	// DefaultPrinterName returns the configured default printer, or an error if
	// none is set.
	DefaultPrinterName() (string, error)
	// SubmitByName ingests a document for the named printer and returns the new
	// job id.
	SubmitByName(ctx context.Context, printerName, jobName string, ticket model.JobTicket, doc io.Reader) (jobID string, err error)
}

// prepared is the state remembered between PreparePrint and Print, keyed by the
// token the backend hands out.
type prepared struct {
	printer string
	ticket  model.JobTicket
	title   string
}

// Backend holds the spooler binding and the token store.
type Backend struct {
	jobs Jobs

	mu       sync.Mutex
	nextTok  uint32
	prepared map[uint32]prepared
}

// New builds a Backend over the given spooler.
func New(jobs Jobs) *Backend {
	return &Backend{jobs: jobs, prepared: make(map[uint32]prepared)}
}

// preparePrint handles the prepare phase: it resolves the target printer and
// the ticket from the GTK settings, stores them under a fresh token, and
// returns the token (plus the echoed settings) so the app can call print. A
// real desktop dialog would refine the choice; headless, we honor the supplied
// settings and fall back to the default printer.
func (b *Backend) preparePrint(title string, settings, pageSetup map[string]string) (uint32, map[string]string, uint32) {
	printer := settings["printer"]
	if printer == "" {
		if def, err := b.jobs.DefaultPrinterName(); err == nil {
			printer = def
		}
	}
	ticket := settingsToTicket(settings, pageSetup)

	b.mu.Lock()
	b.nextTok++
	tok := b.nextTok
	b.prepared[tok] = prepared{printer: printer, ticket: ticket, title: title}
	b.mu.Unlock()

	// Echo the settings back; the app forwards them to print unchanged.
	echo := make(map[string]string, len(settings))
	for k, v := range settings {
		echo[k] = v
	}
	return responseSuccess, echo, tok
}

// print handles the print phase: it looks up the prepared state by token (or
// falls back to the default printer), reads the document and submits it.
func (b *Backend) print(ctx context.Context, title string, doc io.Reader, token uint32, options map[string]string) (uint32, error) {
	b.mu.Lock()
	prep, ok := b.prepared[token]
	if ok {
		delete(b.prepared, token)
	}
	b.mu.Unlock()

	printer := prep.printer
	ticket := prep.ticket
	jobTitle := firstNonEmpty(title, prep.title, "document")

	if printer == "" {
		// No prepared printer (app skipped PreparePrint or token expired): use
		// the default, honoring any settings passed inline with print.
		def, err := b.jobs.DefaultPrinterName()
		if err != nil {
			return responseEnded, fmt.Errorf("printportal: no printer to print to: %w", err)
		}
		printer = def
		ticket = settingsToTicket(options, nil)
	}

	if _, err := b.jobs.SubmitByName(ctx, printer, jobTitle, ticket, doc); err != nil {
		return responseEnded, fmt.Errorf("printportal: submit: %w", err)
	}
	return responseSuccess, nil
}

// settingsToTicket maps the subset of GtkPrintSettings we understand onto a
// Torchio job ticket. GtkPrintSettings serializes every value as a string.
func settingsToTicket(settings, pageSetup map[string]string) model.JobTicket {
	t := model.JobTicket{}
	if n, err := strconv.Atoi(settings["n-copies"]); err == nil && n > 0 {
		t.Copies = n
	}
	switch settings["duplex"] {
	case "simplex":
		t.Sides = "one-sided"
	case "horizontal":
		t.Sides = "two-sided-long-edge"
	case "vertical":
		t.Sides = "two-sided-short-edge"
	}
	// Media size can arrive in either dict under a few keys; take the first.
	for _, k := range []string{"media", "paper-format", "output-paper-format"} {
		if v := settings[k]; v != "" {
			t.Media = v
			break
		}
	}
	if t.Media == "" && pageSetup != nil {
		if v := pageSetup["paper-format"]; v != "" {
			t.Media = v
		}
	}
	if c := settings["output-color-mode"]; c != "" {
		t.Color = c
	}
	return t
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
