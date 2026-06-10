package daemon

import (
	"context"
	"fmt"
	"io"

	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/queue"
	"github.com/singularityos-lab/torchio/internal/store"
)

// spoolerJobs adapts the store and queue engine to the printportal.Jobs
// interface, so the XDG print portal can submit documents to the spooler.
type spoolerJobs struct {
	store  *store.Store
	engine *queue.Engine
}

// DefaultPrinterName returns the configured default printer's name.
func (j spoolerJobs) DefaultPrinterName() (string, error) {
	p, err := j.store.DefaultPrinter()
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

// SubmitByName resolves the printer by name and submits the document.
func (j spoolerJobs) SubmitByName(ctx context.Context, printerName, jobName string, ticket model.JobTicket, doc io.Reader) (string, error) {
	p, err := j.store.GetPrinterByName(printerName)
	if err != nil {
		return "", fmt.Errorf("unknown printer %q: %w", printerName, err)
	}
	job, err := j.engine.Submit(ctx, p.ID, jobName, ticket, doc)
	if err != nil {
		return "", err
	}
	return job.ID.String(), nil
}
