package daemon

import (
	"context"
	"fmt"
	"io"

	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/queue"
	"github.com/singularityos-lab/torchio/internal/store"
)

// cupsBackend adapts the store and queue engine to cupscompat.Backend, so the
// CUPS-compatible front end can enumerate Torchio's printers and submit jobs to
// the spooler.
type cupsBackend struct {
	store  *store.Store
	engine *queue.Engine
}

func (c cupsBackend) ListPrinters() []model.Printer {
	ps, err := c.store.ListPrinters()
	if err != nil {
		return nil
	}
	out := make([]model.Printer, 0, len(ps))
	for _, p := range ps {
		out = append(out, *p)
	}
	return out
}

func (c cupsBackend) DefaultPrinter() (model.Printer, bool) {
	p, err := c.store.DefaultPrinter()
	if err != nil {
		return model.Printer{}, false
	}
	return *p, true
}

func (c cupsBackend) PrinterByName(name string) (model.Printer, bool) {
	p, err := c.store.GetPrinterByName(name)
	if err != nil {
		return model.Printer{}, false
	}
	return *p, true
}

func (c cupsBackend) Submit(ctx context.Context, printerName, jobName string, ticket model.JobTicket, doc io.Reader) (string, error) {
	p, err := c.store.GetPrinterByName(printerName)
	if err != nil {
		return "", fmt.Errorf("unknown printer %q: %w", printerName, err)
	}
	job, err := c.engine.Submit(ctx, p.ID, jobName, ticket, doc)
	if err != nil {
		return "", err
	}
	return job.ID.String(), nil
}

func (c cupsBackend) JobState(spoolerID string) (model.JobState, bool) {
	id, err := model.ParseID(spoolerID)
	if err != nil {
		return "", false
	}
	j, err := c.store.GetJob(id)
	if err != nil {
		return "", false
	}
	return model.JobState(j.State), true
}

func (c cupsBackend) Cancel(ctx context.Context, spoolerID string) error {
	id, err := model.ParseID(spoolerID)
	if err != nil {
		return err
	}
	return c.engine.Cancel(ctx, id)
}
