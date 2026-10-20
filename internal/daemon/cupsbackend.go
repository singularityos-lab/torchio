package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/singularityos-lab/torchio/internal/admin"
	"github.com/singularityos-lab/torchio/internal/cupscompat"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/portal"
	"github.com/singularityos-lab/torchio/internal/queue"
	"github.com/singularityos-lab/torchio/internal/store"
)

// cupsBackend adapts the store, the queue engine and the trust backend to
// cupscompat.Backend.
type cupsBackend struct {
	store  *store.Store
	engine *queue.Engine
	admin  *admin.Admin
	trust  portal.Portal
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

func (c cupsBackend) Submit(ctx context.Context, printer, jobName string, ticket model.JobTicket, doc io.Reader) (cupscompat.Job, error) {
	p, err := c.store.GetPrinterByName(printer)
	if err != nil {
		return cupscompat.Job{}, fmt.Errorf("unknown printer %q: %w", printer, cupscompat.ErrNotFound)
	}
	job, err := c.engine.Submit(ctx, p.ID, jobName, ticket, doc)
	if errors.Is(err, queue.ErrRejecting) {
		return cupscompat.Job{}, fmt.Errorf("%w: %v", cupscompat.ErrNotPossible, err)
	}
	if err != nil {
		return cupscompat.Job{}, err
	}
	return toCupsJob(job, p.Name), nil
}

func (c cupsBackend) Create(ctx context.Context, printer, jobName string, ticket model.JobTicket) (cupscompat.Job, error) {
	p, err := c.store.GetPrinterByName(printer)
	if err != nil {
		return cupscompat.Job{}, fmt.Errorf("unknown printer %q: %w", printer, cupscompat.ErrNotFound)
	}
	job, err := c.engine.Create(ctx, p.ID, jobName, ticket)
	if errors.Is(err, queue.ErrRejecting) {
		return cupscompat.Job{}, fmt.Errorf("%w: %v", cupscompat.ErrNotPossible, err)
	}
	if err != nil {
		return cupscompat.Job{}, err
	}
	return toCupsJob(job, p.Name), nil
}

func (c cupsBackend) Attach(ctx context.Context, number int, doc io.Reader) (cupscompat.Job, error) {
	j, err := c.store.GetJobByNumber(number)
	if err != nil {
		return cupscompat.Job{}, cupscompat.ErrNotFound
	}
	if model.JobState(j.State) != model.JobCreated || j.Document.SHA256 != "" {
		return cupscompat.Job{}, cupscompat.ErrNotPossible
	}
	p, err := c.store.GetPrinter(j.PrinterID)
	if err != nil {
		return cupscompat.Job{}, err
	}
	if err := c.engine.Attach(ctx, j, doc); err != nil {
		return cupscompat.Job{}, err
	}
	return toCupsJob(j, p.Name), nil
}

func (c cupsBackend) Job(number int) (cupscompat.Job, bool) {
	j, err := c.store.GetJobByNumber(number)
	if err != nil {
		return cupscompat.Job{}, false
	}
	p, err := c.store.GetPrinter(j.PrinterID)
	if err != nil {
		return cupscompat.Job{}, false
	}
	return toCupsJob(j, p.Name), true
}

func (c cupsBackend) Jobs(printer, which string) []cupscompat.Job {
	names := map[model.ID]string{}
	ps, err := c.store.ListPrinters()
	if err != nil {
		return nil
	}
	f := store.JobFilter{
		Active:   which == cupscompat.WhichActive,
		Finished: which == cupscompat.WhichFinished,
	}
	for _, p := range ps {
		names[p.ID] = p.Name
		if p.Name == printer {
			f.PrinterID = p.ID
		}
	}
	if printer != "" && f.PrinterID.IsZero() {
		return nil
	}
	jobs, err := c.store.ListJobs(f)
	if err != nil {
		return nil
	}
	out := make([]cupscompat.Job, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toCupsJob(j, names[j.PrinterID]))
	}
	return out
}

func (c cupsBackend) JobAction(ctx context.Context, number int, action string) error {
	j, err := c.store.GetJobByNumber(number)
	if err != nil {
		return cupscompat.ErrNotFound
	}
	state := model.JobState(j.State)
	switch action {
	case cupscompat.ActionCancel:
		if state.IsTerminal() {
			return cupscompat.ErrNotPossible
		}
		return c.engine.Cancel(ctx, j.ID)
	case cupscompat.ActionHold:
		if state == model.JobHeld {
			return nil
		}
		if !model.CanTransition(state, model.JobHeld) {
			return cupscompat.ErrNotPossible
		}
		return c.engine.Hold(ctx, j.ID)
	case cupscompat.ActionRelease:
		if state != model.JobHeld {
			return cupscompat.ErrNotPossible
		}
		return c.engine.Release(ctx, j.ID)
	case cupscompat.ActionRestart:
		if !state.IsTerminal() || !model.CanTransition(state, model.JobPending) {
			return cupscompat.ErrNotPossible
		}
		return c.engine.Restart(ctx, j.ID)
	}
	return cupscompat.ErrNotPossible
}

// AddOrModifyPrinter creates a queue only after the trust backend approves the
// device, the same rule a discovered printer follows.
func (c cupsBackend) AddOrModifyPrinter(ctx context.Context, name string, ch cupscompat.PrinterChange) error {
	if _, err := c.store.GetPrinterByName(name); errors.Is(err, store.ErrNotFound) {
		if ch.URI == nil || *ch.URI == "" {
			return fmt.Errorf("%w: a new printer needs a device-uri", cupscompat.ErrNotPossible)
		}
		reason := fmt.Sprintf("Add the printer %q?", name)
		decision, err := c.trust.RequestPermission(ctx, portal.CategoryNetwork, *ch.URI, reason)
		if err != nil {
			return err
		}
		if !decision.Granted() {
			return fmt.Errorf("%w: adding %q was not approved", cupscompat.ErrNotPossible, name)
		}
		if _, err := c.admin.Add(ctx, name, *ch.URI, model.OriginManual, false); err != nil {
			return err
		}
		ch.URI = nil
	}
	return c.admin.Update(name, func(p *model.Printer) { applyChange(p, ch) })
}

func applyChange(p *model.Printer, ch cupscompat.PrinterChange) {
	q := &p.Queue
	if ch.URI != nil && *ch.URI != "" {
		p.URI = *ch.URI
	}
	if ch.Info != nil {
		q.Info = *ch.Info
	}
	if ch.Location != nil {
		q.Location = *ch.Location
	}
	if ch.Shared != nil {
		q.Shared = *ch.Shared
	}
	if ch.Accepting != nil {
		q.Rejecting = !*ch.Accepting
	}
	if ch.Paused != nil {
		q.Paused = *ch.Paused
		q.Message = ""
	}
	if ch.Message != nil {
		q.Message = *ch.Message
	}
	d := ch.Defaults
	if d.Media != "" {
		q.Defaults.Media = d.Media
	}
	if d.Sides != "" {
		q.Defaults.Sides = d.Sides
	}
	if d.Color != "" {
		q.Defaults.Color = d.Color
	}
	if d.Quality != 0 {
		q.Defaults.Quality = d.Quality
	}
	if d.Copies != 0 {
		q.Defaults.Copies = d.Copies
	}
}

func (c cupsBackend) DeletePrinter(name string) error {
	if err := c.admin.Remove(name); errors.Is(err, store.ErrNotFound) {
		return cupscompat.ErrNotFound
	} else {
		return err
	}
}

func (c cupsBackend) SetDefault(name string) error {
	if err := c.admin.SetDefault(name); errors.Is(err, store.ErrNotFound) {
		return cupscompat.ErrNotFound
	} else {
		return err
	}
}

// Devices lists what discovery has seen recently and is not configured yet.
func (c cupsBackend) Devices(ctx context.Context) []cupscompat.Device {
	seen, err := c.store.ListDevices(time.Now().Add(-10 * time.Minute))
	if err != nil {
		return nil
	}
	configured := map[string]bool{}
	for _, p := range c.ListPrinters() {
		configured[p.URI] = true
	}
	var out []cupscompat.Device
	for _, d := range seen {
		if configured[d.URI] {
			continue
		}
		out = append(out, cupscompat.Device{URI: d.URI, Info: d.Name, MakeModel: d.Name, Class: "network"})
	}
	return out
}

func toCupsJob(j *model.Job, printer string) cupscompat.Job {
	return cupscompat.Job{
		Number:  j.Number,
		Printer: printer,
		Name:    j.Name,
		State:   model.JobState(j.State),
		Size:    j.Document.Size,
		Created: j.Created,
		Updated: j.Updated,
	}
}
