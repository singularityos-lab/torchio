package queue

import "github.com/singularityos-lab/torchio/internal/model"

// JobEvent describes one change in a job's history, delivered to the engine's
// Notify callback.
type JobEvent struct {
	JobID     model.ID
	PrinterID model.ID
	From      model.JobState
	To        model.JobState
	Cause     string
}
