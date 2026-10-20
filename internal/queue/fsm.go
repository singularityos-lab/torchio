package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/singularityos-lab/torchio/internal/model"
)

// transition is the only way a job's state changes. Keeping it in one place
// guarantees that every move is legal and that the history is complete.
func (e *Engine) transition(ctx context.Context, job *model.Job, to model.JobState, actor, cause string) error {
	from := model.JobState(job.State)
	if !model.CanTransition(from, to) {
		return fmt.Errorf("queue: illegal transition %s->%s for job %s", from, to, job.ID)
	}
	job.State = string(to)
	job.Updated = time.Now()
	if to == model.JobAborted {
		job.Error = cause
	}
	ev := newEvent(job, model.EventTransition, from, to, actor, cause)
	if err := e.store.SaveTransition(job, ev); err != nil {
		return fmt.Errorf("queue: persist job %s: %w", job.ID, err)
	}
	e.publish(job, ev)
	return nil
}

// appendEvent records a note that does not change the job's state.
func (e *Engine) appendEvent(ctx context.Context, job *model.Job, kind model.EventKind, from, to model.JobState, actor, cause string) {
	ev := newEvent(job, kind, from, to, actor, cause)
	if err := e.store.AppendEvent(ev); err != nil {
		e.log.Printf("append event for job %s: %v", job.ID, err)
	}
	e.publish(job, ev)
}

func newEvent(job *model.Job, kind model.EventKind, from, to model.JobState, actor, cause string) *model.Event {
	return &model.Event{
		JobID: job.ID,
		At:    time.Now(),
		Kind:  kind,
		From:  from,
		To:    to,
		Actor: actor,
		Cause: cause,
	}
}

func (e *Engine) publish(job *model.Job, ev *model.Event) {
	if e.notify == nil {
		return
	}
	go e.notify(JobEvent{
		JobID:     job.ID,
		PrinterID: job.PrinterID,
		From:      ev.From,
		To:        ev.To,
		Cause:     ev.Cause,
	})
}
