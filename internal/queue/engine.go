// Package queue is Torchio's spooler core. It implements the per-printer actor
// model: one supervising goroutine owns each printer's queue, so jobs for a
// single printer are serialized (they leave in order) while different printers
// run in parallel, with no shared locks between queues. State changes go
// through the go-foundation FSM, are persisted to the store, logged to the
// append-only event table and emitted on the event bus.
package queue

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mirkobrombin/go-foundation/pkg/events"
	"github.com/mirkobrombin/go-foundation/pkg/logger"

	"github.com/singularityos-lab/torchio/internal/detect"
	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/spool"
	"github.com/singularityos-lab/torchio/internal/store"
	"github.com/singularityos-lab/torchio/internal/transform"
)

// Transport is the slice of the IPP client the queue needs. Defining it as an
// interface lets tests inject a fake printer without a network.
type Transport interface {
	PrintJob(ctx context.Context, r ipp.PrintRequest) (*ipp.PrintResult, error)
}

// Engine owns the set of per-printer actors and the shared dependencies.
type Engine struct {
	store      *store.Store
	spool      *spool.Spool
	bus        *events.Bus
	transport  Transport
	transforms *transform.Registry
	log        logger.Logger

	// baseCtx is the daemon-lifetime context under which actors run. It is set
	// by Start and is deliberately decoupled from the short-lived per-RPC
	// contexts, so adding a printer or submitting a job does not tie an actor's
	// lifetime to a single call.
	baseCtx context.Context

	mu     sync.Mutex
	actors map[string]*actor // keyed by printer ID string
	wg     sync.WaitGroup
}

// Config wires an Engine's dependencies.
type Config struct {
	Store     *store.Store
	Spool     *spool.Spool
	Bus       *events.Bus
	Transport Transport
	// Transforms is the format-conversion registry. Nil means the built-in
	// native transforms (DefaultRegistry).
	Transforms *transform.Registry
	Logger     logger.Logger
}

// New builds an Engine. All fields of cfg except Logger and Transforms are
// required.
func New(cfg Config) *Engine {
	log := cfg.Logger
	if log == nil {
		log = logger.New()
	}
	reg := cfg.Transforms
	if reg == nil {
		reg = transform.DefaultRegistry()
	}
	return &Engine{
		store:      cfg.Store,
		spool:      cfg.Spool,
		bus:        cfg.Bus,
		transport:  cfg.Transport,
		transforms: reg,
		log:        log,
		actors:     make(map[string]*actor),
	}
}

// Start spawns an actor per known printer and recovers any jobs that were
// in flight when the daemon last stopped.
func (e *Engine) Start(ctx context.Context) error {
	e.baseCtx = ctx
	printers, err := e.store.ListPrinters()
	if err != nil {
		return fmt.Errorf("queue: load printers: %w", err)
	}
	for _, p := range printers {
		e.ensureActor(p)
	}
	if err := e.recover(ctx); err != nil {
		return fmt.Errorf("queue: recover: %w", err)
	}
	return nil
}

// Stop closes all actor inboxes and waits for them to drain.
func (e *Engine) Stop() {
	e.mu.Lock()
	for _, a := range e.actors {
		a.close()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

// ensureActor returns the actor for a printer, starting it if needed. Actors
// always run under the engine's base context, never a per-call one.
func (e *Engine) ensureActor(p *model.Printer) *actor {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := p.ID.String()
	if a, ok := e.actors[key]; ok {
		a.printer = p // refresh cached printer (URI/caps may have changed)
		return a
	}
	ctx := e.baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	a := newActor(e, p)
	e.actors[key] = a
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		a.run(ctx)
	}()
	return a
}

// AddPrinter registers a printer with the engine at runtime and starts its
// actor. It does not persist the printer; callers save it via the store first.
func (e *Engine) AddPrinter(p *model.Printer) {
	e.ensureActor(p)
}

// Submit ingests a document for a printer: it sniffs the real format, stores
// the bytes in the content-addressed spool, creates the job in state "created",
// and hands it to the printer's actor. It returns the created job.
func (e *Engine) Submit(ctx context.Context, printerID model.ID, name string, ticket model.JobTicket, doc io.Reader) (*model.Job, error) {
	p, err := e.store.GetPrinter(printerID)
	if err != nil {
		return nil, fmt.Errorf("queue: unknown printer: %w", err)
	}

	// Sniff before storing so a malformed file is caught at the cheapest
	// possible point, and the detector replays the full stream into the spool.
	format, body, err := detect.Format(doc)
	if err != nil {
		return nil, fmt.Errorf("queue: sniff: %w", err)
	}
	digest, size, err := e.spool.Put(body)
	if err != nil {
		return nil, fmt.Errorf("queue: spool: %w", err)
	}

	now := time.Now()
	job := &model.Job{
		ID:        model.NewID(),
		PrinterID: printerID,
		Name:      name,
		State:     string(model.JobCreated),
		Ticket:    ticket,
		Document:  model.DocRef{SHA256: digest, Format: format, Size: size},
		Created:   now,
		Updated:   now,
	}
	if err := e.store.SaveJob(job); err != nil {
		return nil, fmt.Errorf("queue: save job: %w", err)
	}
	e.appendEvent(ctx, job, model.EventNote, "", "", "user", "submitted")

	a := e.ensureActor(p)
	a.enqueue(command{kind: cmdSubmit, jobID: job.ID})
	return job, nil
}

// Cancel requests cancellation of a job. The owning actor performs the
// transition so it stays serialized with the job's processing.
func (e *Engine) Cancel(ctx context.Context, jobID model.ID) error {
	job, err := e.store.GetJob(jobID)
	if err != nil {
		return err
	}
	if model.JobState(job.State).IsTerminal() {
		return fmt.Errorf("queue: job %s already %s", jobID, job.State)
	}
	e.mu.Lock()
	a := e.actors[job.PrinterID.String()]
	e.mu.Unlock()
	if a == nil {
		return fmt.Errorf("queue: no actor for printer %s", job.PrinterID)
	}
	a.enqueue(command{kind: cmdCancel, jobID: jobID})
	return nil
}

// recover requeues jobs that were mid-flight at shutdown. A job that was
// "sending" is moved back to "pending" (the IPP send is idempotent on our
// side: we reconcile with the printer before re-sending). Other non-terminal
// jobs are simply re-enqueued.
func (e *Engine) recover(ctx context.Context) error {
	jobs, err := e.store.ListJobs(store.JobFilter{Active: true})
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if model.JobState(job.State) == model.JobSending {
			if err := e.transition(ctx, job, model.JobPending, "spooler", "requeued after restart"); err != nil {
				e.log.Warn("recover requeue failed", logger.Field{Key: "job", Value: job.ID.String()}, logger.Field{Key: "err", Value: err.Error()})
				continue
			}
		}
		e.mu.Lock()
		a := e.actors[job.PrinterID.String()]
		e.mu.Unlock()
		if a != nil {
			a.enqueue(command{kind: cmdSubmit, jobID: job.ID})
		}
	}
	return nil
}
