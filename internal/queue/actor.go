package queue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/singularityos-lab/torchio/internal/ipp"
	"github.com/singularityos-lab/torchio/internal/model"
	"github.com/singularityos-lab/torchio/internal/store"
	"github.com/singularityos-lab/torchio/internal/transform"
)

// commandKind enumerates the messages an actor accepts.
type commandKind int

const (
	cmdSubmit commandKind = iota // process (or resume) a job
	cmdCancel
	cmdHold
	cmdRelease
	cmdRestart
	// cmdResume re-reads the printer and restarts its waiting jobs.
	cmdResume
)

// command is one message in an actor's inbox.
type command struct {
	kind  commandKind
	jobID model.ID
}

// actor owns one printer's queue. A single goroutine drains the inbox, so all
// work for a printer is serialized: jobs leave in order and there are no shared
// locks between printers.
type actor struct {
	engine  *Engine
	printer *model.Printer
	inbox   chan command

	mu     sync.Mutex
	closed bool
}

// inboxSize bounds how many commands can queue before Submit blocks. Generous,
// since a desktop rarely backs up thousands of jobs per printer.
const inboxSize = 256

func newActor(e *Engine, p *model.Printer) *actor {
	return &actor{
		engine:  e,
		printer: p,
		inbox:   make(chan command, inboxSize),
	}
}

// enqueue posts a command to the actor. It blocks only if the inbox is full,
// and drops the command once the actor is closed.
func (a *actor) enqueue(c command) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.inbox <- c
	}
}

// close stops the actor after it drains in-flight commands.
func (a *actor) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		close(a.inbox)
	}
}

// run is the actor goroutine. It exits when the inbox is closed or the context
// is cancelled.
func (a *actor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-a.inbox:
			if !ok {
				return
			}
			a.handle(ctx, c)
		}
	}
}

func (a *actor) handle(ctx context.Context, c command) {
	switch c.kind {
	case cmdSubmit:
		a.process(ctx, c.jobID)
	case cmdCancel:
		a.cancel(ctx, c.jobID)
	case cmdHold:
		a.move(ctx, c.jobID, model.JobHeld, "held by user")
	case cmdRelease:
		if a.release(c.jobID) && a.move(ctx, c.jobID, model.JobPending, "released by user") {
			a.process(ctx, c.jobID)
		}
	case cmdRestart:
		if a.move(ctx, c.jobID, model.JobPending, "restarted by user") {
			a.process(ctx, c.jobID)
		}
	case cmdResume:
		a.resume(ctx)
	}
}

// move applies a user-requested transition and reports whether it happened.
func (a *actor) move(ctx context.Context, jobID model.ID, to model.JobState, cause string) bool {
	job, err := a.engine.store.GetJob(jobID)
	if err != nil {
		return false
	}
	if !model.CanTransition(model.JobState(job.State), to) {
		return false
	}
	return a.step(ctx, job, to, "user", cause) == nil
}

// release clears the hold request so the job prints once it is pending.
func (a *actor) release(jobID model.ID) bool {
	job, err := a.engine.store.GetJob(jobID)
	if err != nil || model.JobState(job.State) != model.JobHeld {
		return false
	}
	job.Ticket.Hold = false
	return a.engine.store.SaveJob(job) == nil
}

// resume picks up the printer's new settings and processes every job that
// was waiting for it.
func (a *actor) resume(ctx context.Context) {
	if !a.refresh() || a.printer.Queue.Paused {
		return
	}
	jobs, err := a.engine.store.ListJobs(store.JobFilter{PrinterID: a.printer.ID, Active: true})
	if err != nil {
		a.engine.log.Printf("resume %s: %v", a.printer.Name, err)
		return
	}
	for _, j := range jobs {
		if model.JobState(j.State) != model.JobHeld {
			a.process(ctx, j.ID)
		}
	}
}

// refresh reloads the printer so the actor sees settings changed elsewhere.
func (a *actor) refresh() bool {
	p, err := a.engine.store.GetPrinter(a.printer.ID)
	if err != nil {
		return false
	}
	a.printer = p
	return true
}

// cancel transitions a non-terminal job to canceled. A job being streamed is
// not interrupted: the command waits in the inbox until the send returns.
func (a *actor) cancel(ctx context.Context, jobID model.ID) {
	job, err := a.engine.store.GetJob(jobID)
	if err != nil {
		return
	}
	if model.JobState(job.State).IsTerminal() {
		return
	}
	if err := a.engine.transition(ctx, job, model.JobCanceled, "user", "cancelled by user"); err != nil {
		a.engine.log.Printf("cancel failed: %v", err)
	}
}

// process advances a job through the FSM to a terminal state. It reloads the
// job fresh (it may have been cancelled while queued) and stops at the first
// state that cannot advance without external input.
func (a *actor) process(ctx context.Context, jobID model.ID) {
	job, err := a.engine.store.GetJob(jobID)
	if err != nil {
		a.engine.log.Printf("process: load job: %v", err)
		return
	}
	if !a.refresh() {
		return
	}
	if model.JobState(job.State) == model.JobCreated && job.Document.SHA256 == "" {
		return
	}

	for !model.JobState(job.State).IsTerminal() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		switch model.JobState(job.State) {
		case model.JobCreated:
			if err := a.step(ctx, job, model.JobValidating, "spooler", "validating"); err != nil {
				return
			}
		case model.JobValidating:
			if !a.validate(ctx, job) {
				return // aborted inside validate
			}
		case model.JobPending:
			if job.Ticket.Hold {
				_ = a.step(ctx, job, model.JobHeld, "user", "held until released")
				return
			}
			if a.printer.Queue.Paused {
				return
			}
			if !a.prepare(ctx, job) {
				return // aborted inside prepare
			}
		case model.JobSpooled:
			if a.printer.Queue.Paused {
				return
			}
			if !a.send(ctx, job) {
				return // aborted inside send
			}
		case model.JobHeld:
			return
		default:
			a.engine.log.Printf("process: unexpected state %s", job.State)
			return
		}
	}
}

// validate checks that the document can reach the printer: either the printer
// accepts the format directly, or the transform graph has a path to a format it
// accepts. If neither, the job is aborted (nothing can be done with it).
func (a *actor) validate(ctx context.Context, job *model.Job) bool {
	caps := a.printer.Caps
	format := job.Document.Format

	// With no capability data we cannot plan; assume the printer accepts it
	// (best effort) and let the send surface any rejection.
	if len(caps.DocumentFormats) == 0 {
		return a.step(ctx, job, model.JobPending, "spooler", "validated (caps unknown)") == nil
	}

	if _, ok := a.engine.transforms.Plan(format, caps.DocumentFormats); !ok {
		cause := fmt.Sprintf("printer does not accept %s and no transform path exists", format)
		_ = a.step(ctx, job, model.JobAborted, "spooler", cause)
		return false
	}
	return a.step(ctx, job, model.JobPending, "spooler", "validated") == nil
}

// prepare turns the pending job into a spooled one. It plans the format path:
// an empty plan is the fast path (printer accepts the format, document passes
// verbatim); a non-empty plan runs the transform hops and replaces the job's
// document with the converted bytes.
func (a *actor) prepare(ctx context.Context, job *model.Job) bool {
	caps := a.printer.Caps

	var plan []transform.Transform
	if len(caps.DocumentFormats) > 0 {
		p, ok := a.engine.transforms.Plan(job.Document.Format, caps.DocumentFormats)
		if !ok {
			_ = a.step(ctx, job, model.JobAborted, "spooler", "no transform path")
			return false
		}
		plan = p
	}

	if len(plan) == 0 {
		// Fast path: nothing to convert.
		return a.step(ctx, job, model.JobSpooled, "spooler", "fast path, no transform") == nil
	}

	if err := a.step(ctx, job, model.JobTransforming, "spooler",
		fmt.Sprintf("%d-hop transform to %s", len(plan), transform.FinalFormat(plan, job.Document.Format))); err != nil {
		return false
	}
	if !a.runTransform(ctx, job, plan) {
		return false // aborted inside
	}
	return a.step(ctx, job, model.JobSpooled, "spooler", "transformed") == nil
}

// runTransform executes the plan, streaming the spooled document through each
// hop, and replaces job.Document with the converted blob. The new document is
// persisted by the subsequent transition to spooled.
func (a *actor) runTransform(ctx context.Context, job *model.Job, plan []transform.Transform) bool {
	rc, err := a.engine.spool.Open(job.Document.SHA256)
	if err != nil {
		_ = a.step(ctx, job, model.JobAborted, "spooler", "open document: "+err.Error())
		return false
	}
	defer rc.Close()

	params := transform.Params{Ticket: job.Ticket, Caps: a.printer.Caps}
	var cur io.Reader = rc
	for _, t := range plan {
		// Buffer between hops. Documents are page-sized in practice; streaming
		// across hops is a later optimisation for very large jobs.
		var buf bytes.Buffer
		if err := t.Run(ctx, cur, &buf, params); err != nil {
			e := t.Edge()
			_ = a.step(ctx, job, model.JobAborted, "spooler",
				fmt.Sprintf("transform %s->%s: %s", e.From, e.To, err.Error()))
			return false
		}
		cur = &buf
	}

	digest, size, err := a.engine.spool.Put(cur)
	if err != nil {
		_ = a.step(ctx, job, model.JobAborted, "spooler", "spool transformed: "+err.Error())
		return false
	}
	job.Document = model.DocRef{
		SHA256: digest,
		Format: transform.FinalFormat(plan, job.Document.Format),
		Size:   size,
	}
	return true
}

// send streams the spooled document to the printer over IPP, with bounded
// retries on transient transport errors. A printer-reported IPP status error is
// permanent and aborts immediately.
func (a *actor) send(ctx context.Context, job *model.Job) bool {
	if err := a.step(ctx, job, model.JobSending, "spooler", "sending to printer"); err != nil {
		return false
	}

	var result *ipp.PrintResult
	err := retry(ctx, sendAttempts, func() error {
		rc, openErr := a.engine.spool.Open(job.Document.SHA256)
		if openErr != nil {
			return permanent{openErr} // a missing blob will not fix itself
		}
		defer rc.Close()

		req := ipp.PrintRequest{
			PrinterURI:     a.printer.URI,
			JobName:        job.Name,
			DocumentFormat: job.Document.Format.String(),
			Copies:         job.Ticket.Copies,
			Sides:          job.Ticket.Sides,
			Media:          job.Ticket.Media,
			Document:       rc,
		}
		r, sendErr := a.engine.transport.PrintJob(ctx, req)
		if sendErr != nil {
			if _, ok := ipp.AsStatusError(sendErr); ok {
				return permanent{sendErr} // the printer rejected it; do not retry
			}
			return sendErr // transient, retry
		}
		result = r
		return nil
	})

	if err != nil {
		_ = a.step(ctx, job, model.JobAborted, "printer", "send failed: "+err.Error())
		return false
	}

	if result != nil {
		job.RemoteJobID = result.JobID
	}
	a.engine.log.Printf("job %s sent to %s as job %d", job.ID, a.printer.Name, job.RemoteJobID)
	return a.step(ctx, job, model.JobCompleted, "printer", "accepted by printer") == nil
}

// step performs one FSM transition and logs on failure. It returns the error so
// callers can stop the processing loop.
func (a *actor) step(ctx context.Context, job *model.Job, to model.JobState, actor, cause string) error {
	if err := a.engine.transition(ctx, job, to, actor, cause); err != nil {
		a.engine.log.Printf("transition failed: %v", err)
		return err
	}
	return nil
}

// permanent marks an error that retries cannot fix.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

const sendAttempts = 3

// retry runs fn up to attempts times, backing off between tries, and stops at
// the first success, permanent error or cancellation.
func retry(ctx context.Context, attempts int, fn func() error) error {
	var err error
	delay := 500 * time.Millisecond
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		var p permanent
		if errors.As(err, &p) || i == attempts-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	return err
}
