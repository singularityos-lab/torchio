package queue

import (
	"testing"

	"github.com/mirkobrombin/go-foundation/pkg/fsm/machine"

	"github.com/singularityos-lab/torchio/internal/model"
)

// TestJobBindsToFSM confirms the go-foundation FSM binds to model.Job's State
// field even though it is not the first field of the struct (it follows ID,
// PrinterID and Name). This exercises the upstream fix in go-foundation
// v1.1.1; with an older version the machine bound to the wrong field.
func TestJobBindsToFSM(t *testing.T) {
	job := &model.Job{ID: model.NewID(), Name: "x", State: string(model.JobCreated)}
	m, err := machine.New(job)
	if err != nil {
		t.Fatalf("machine.New(job): %v", err)
	}
	if got := m.CurrentState(); got != string(model.JobCreated) {
		t.Fatalf("CurrentState = %q, want %q (FSM bound to the wrong field?)", got, model.JobCreated)
	}
	if err := m.Transition(string(model.JobValidating)); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if job.State != string(model.JobValidating) {
		t.Errorf("job.State = %q, want validating", job.State)
	}
}
