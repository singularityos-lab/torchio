package queue

import (
	"testing"

	"github.com/singularityos-lab/torchio/internal/model"
)

func TestJobTransitions(t *testing.T) {
	cases := []struct {
		from, to model.JobState
		ok       bool
	}{
		{model.JobCreated, model.JobValidating, true},
		{model.JobPending, model.JobSpooled, true},
		{model.JobSending, model.JobPending, true},
		{model.JobHeld, model.JobPending, true},
		{model.JobCreated, model.JobSending, false},
		{model.JobCompleted, model.JobPending, false},
		{model.JobSending, model.JobCanceled, true},
		{model.JobCompleted, model.JobCanceled, false},
	}
	for _, c := range cases {
		if got := model.CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("%s->%s = %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}
