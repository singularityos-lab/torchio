// Package admin holds the printer management operations shared by the control
// socket and the CUPS-compatible socket, so both apply the same rules.
package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/queue"
	"github.com/singularityos-lab/torchio/internal/store"
)

// Admin manages the configured printers.
type Admin struct {
	store  *store.Store
	engine *queue.Engine
	prober *ipp.Client
}

// New wires the operations to the daemon's store and engine.
func New(st *store.Store, eng *queue.Engine) *Admin {
	return &Admin{store: st, engine: eng, prober: ipp.NewClient()}
}

// Add probes the printer at uri, stores it under name and starts its queue.
func (a *Admin) Add(ctx context.Context, name, uri string, origin model.Origin, makeDefault bool) (*model.Printer, error) {
	if name == "" || uri == "" {
		return nil, fmt.Errorf("name and uri are required")
	}
	if _, err := a.store.GetPrinterByName(name); err == nil {
		return nil, fmt.Errorf("a printer named %q already exists", name)
	}
	msg, err := a.prober.GetPrinterAttributes(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", uri, err)
	}
	raw, _ := msg.EncodeBytes()
	now := time.Now()
	p := &model.Printer{
		ID:       model.NewID(),
		Name:     name,
		URI:      uri,
		Origin:   origin,
		Caps:     ipp.ParseCapabilities(msg),
		RawAttrs: raw,
		Created:  now,
		Updated:  now,
	}
	if err := a.store.SavePrinter(p); err != nil {
		return nil, fmt.Errorf("save printer: %w", err)
	}
	if _, err := a.store.DefaultPrinter(); makeDefault || errors.Is(err, store.ErrNotFound) {
		if err := a.store.SetDefaultPrinter(p.ID); err != nil {
			return nil, err
		}
		p.Default = true
	}
	a.engine.AddPrinter(p)
	return p, nil
}

// Remove deletes a printer and its jobs.
func (a *Admin) Remove(name string) error {
	p, err := a.store.GetPrinterByName(name)
	if err != nil {
		return err
	}
	a.engine.RemovePrinter(p.ID)
	return a.store.DeletePrinter(p.ID)
}

// SetDefault makes name the default printer.
func (a *Admin) SetDefault(name string) error {
	p, err := a.store.GetPrinterByName(name)
	if err != nil {
		return err
	}
	return a.store.SetDefaultPrinter(p.ID)
}

// Update changes a printer's queue settings or URI and tells its queue.
func (a *Admin) Update(name string, change func(*model.Printer)) error {
	p, err := a.store.GetPrinterByName(name)
	if err != nil {
		return err
	}
	change(p)
	p.Updated = time.Now()
	if err := a.store.SavePrinter(p); err != nil {
		return err
	}
	a.engine.PrinterChanged(p)
	return nil
}

// SetPaused pauses or resumes a queue.
func (a *Admin) SetPaused(name string, paused bool) error {
	return a.Update(name, func(p *model.Printer) { p.Queue.Paused = paused })
}

// SetAccepting makes a queue accept or refuse new jobs.
func (a *Admin) SetAccepting(name string, accepting bool) error {
	return a.Update(name, func(p *model.Printer) { p.Queue.Rejecting = !accepting })
}
