package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-foundation/pkg/events"
	"github.com/mirkobrombin/go-foundation/pkg/fsm/machine"

	"github.com/singularityos-lab/torchio/internal/model"
)

// transition is the ONLY way a job's state changes. It validates the move with
// the go-foundation FSM bound directly to the job (the FSM tag on
// model.Job.State is the single source of truth), persists the job, appends a
// transition event and emits on the bus. Centralising this guarantees the
// history is always complete and no illegal transition slips through.
func (e *Engine) transition(ctx context.Context, job *model.Job, to model.JobState, actor, cause string) error {
	from := model.JobState(job.State)

	m, err := machine.New(job)
	if err != nil {
		return fmt.Errorf("queue: build FSM for job %s: %w", job.ID, err)
	}
	if err := m.Transition(string(to)); err != nil {
		return fmt.Errorf("queue: illegal transition %s->%s for job %s: %w", from, to, job.ID, err)
	}
	// The machine mutated job.State in place.

	job.Updated = time.Now()
	if to == model.JobAborted {
		job.Error = cause
	}
	if err := e.store.SaveJob(job); err != nil {
		return fmt.Errorf("queue: persist job %s: %w", job.ID, err)
	}

	e.appendEvent(ctx, job, model.EventTransition, from, to, actor, cause)
	return nil
}

// appendEvent writes one history row and emits the corresponding bus event.
// Emission is async so a slow subscriber (e.g. a streaming client) never
// stalls job processing.
func (e *Engine) appendEvent(ctx context.Context, job *model.Job, kind model.EventKind, from, to model.JobState, actor, cause string) {
	ev := &model.Event{
		JobID: job.ID,
		At:    time.Now(),
		Kind:  kind,
		From:  from,
		To:    to,
		Actor: actor,
		Cause: cause,
	}
	if err := e.store.AppendEvent(ev); err != nil {
		// History is best-effort relative to the state change, which is
		// already durably saved; log and continue.
		e.log.Warn("append event failed: " + err.Error())
	}
	events.EmitAsync(ctx, e.bus, JobEvent{
		JobID:     job.ID,
		PrinterID: job.PrinterID,
		From:      from,
		To:        to,
		Cause:     cause,
	})
}
