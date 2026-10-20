package control

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/singularityos-lab/torchio/internal/admin"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/portal"
	"github.com/singularityos-lab/torchio/internal/queue"
	"github.com/singularityos-lab/torchio/internal/store"
)

// Service is the RPC receiver. Its exported methods follow the net/rpc shape
// (args, *reply) error and become "Service.Method" on the wire. It is the only
// API surface of the daemon: the CLI, desktop and portal are all clients of it.
type Service struct {
	store  *store.Store
	engine *queue.Engine
	admin  *admin.Admin
	portal portal.Portal
}

// NewService wires the service to the daemon's store, engine, an IPP client
// used to probe printer capabilities, and the ush portal used to gate trust
// decisions (adding a discovered printer). A nil portal denies such decisions.
func NewService(st *store.Store, eng *queue.Engine, p portal.Portal) *Service {
	if p == nil {
		p = portal.AlwaysDeny{}
	}
	return &Service{store: st, engine: eng, admin: admin.New(st, eng), portal: p}
}

// ctx returns a bounded context for an RPC call. net/rpc has no per-call
// context, so the service derives a generous one.
func (s *Service) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// AddPrinter probes a printer's capabilities, persists it and starts its actor.
func (s *Service) AddPrinter(args AddPrinterArgs, reply *PrinterReply) error {
	ctx, cancel := s.ctx()
	defer cancel()
	p, err := s.admin.Add(ctx, args.Name, args.URI, model.OriginManual, args.MakeDefault)
	if err != nil {
		return err
	}
	reply.Printer = toPrinterDTO(p)
	return nil
}

// ListPrinters returns all configured printers.
func (s *Service) ListPrinters(_ Empty, reply *PrintersReply) error {
	ps, err := s.store.ListPrinters()
	if err != nil {
		return err
	}
	for _, p := range ps {
		reply.Printers = append(reply.Printers, toPrinterDTO(p))
	}
	return nil
}

// RemovePrinter deletes a printer (and its jobs, via cascade).
func (s *Service) RemovePrinter(args NameArgs, _ *Empty) error {
	return s.admin.Remove(args.Name)
}

// SetDefault marks a printer as the default by name.
func (s *Service) SetDefault(args NameArgs, _ *Empty) error {
	return s.admin.SetDefault(args.Name)
}

// Submit sends a document to a printer through the spooler.
func (s *Service) Submit(args SubmitArgs, reply *SubmitReply) error {
	p, err := s.resolvePrinter(args.Printer)
	if err != nil {
		return err
	}
	ctx, cancel := s.ctx()
	defer cancel()

	ticket := model.JobTicket{
		Copies: args.Ticket.Copies,
		Sides:  args.Ticket.Sides,
		Media:  args.Ticket.Media,
		Pages:  args.Ticket.Pages,
		Color:  args.Ticket.Color,
	}
	job, err := s.engine.Submit(ctx, p.ID, args.Name, ticket, bytes.NewReader(args.Doc))
	if err != nil {
		return err
	}
	reply.JobID = job.ID.String()
	return nil
}

// ListJobs returns jobs, optionally filtered by printer and active-only.
func (s *Service) ListJobs(args ListJobsArgs, reply *JobsReply) error {
	f := store.JobFilter{Active: args.Active}
	if args.Printer != "" {
		p, err := s.store.GetPrinterByName(args.Printer)
		if err != nil {
			return err
		}
		f.PrinterID = p.ID
	}
	jobs, err := s.store.ListJobs(f)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		reply.Jobs = append(reply.Jobs, toJobDTO(j))
	}
	return nil
}

// CancelJob requests cancellation of a job by its ULID string.
func (s *Service) CancelJob(args IDArgs, _ *Empty) error {
	id, err := model.ParseID(args.ID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	return s.engine.Cancel(ctx, id)
}

// JobHistory returns a job and its event log.
func (s *Service) JobHistory(args IDArgs, reply *HistoryReply) error {
	id, err := model.ParseID(args.ID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	job, err := s.store.GetJob(id)
	if err != nil {
		return err
	}
	events, err := s.store.JobEvents(id)
	if err != nil {
		return err
	}
	reply.Job = toJobDTO(job)
	for _, e := range events {
		reply.Events = append(reply.Events, toEventDTO(e))
	}
	return nil
}

// resolvePrinter finds a printer by name, or the default when name is empty.
func (s *Service) resolvePrinter(name string) (*model.Printer, error) {
	if name == "" {
		p, err := s.store.DefaultPrinter()
		if err == store.ErrNotFound {
			return nil, fmt.Errorf("no printer specified and no default set")
		}
		return p, err
	}
	return s.store.GetPrinterByName(name)
}
