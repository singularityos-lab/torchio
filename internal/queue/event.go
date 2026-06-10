package queue

import "github.com/singularityos-lab/torchio/internal/model"

// JobEvent is published on the go-foundation event bus whenever a job changes
// state. The SQLite event log and the live gRPC stream to the desktop are both
// just subscribers to this one flow: the daemon does not maintain two parallel
// notification mechanisms. (See docs/FOUNDATION.md.)
type JobEvent struct {
	JobID     model.ID
	PrinterID model.ID
	From      model.JobState
	To        model.JobState
	Cause     string
}
