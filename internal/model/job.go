package model

import "time"

// JobState is the lifecycle state of a job. The values are chosen to map 1:1
// onto IPP "job-state" so that sharing a queue is a projection, not a
// translation. The string values double as the FSM tag tokens on Job.State.
type JobState string

const (
	// JobCreated: accepted, document not yet validated.
	JobCreated JobState = "created"
	// JobValidating: sniffing the format and checking the ticket.
	JobValidating JobState = "validating"
	// JobPending: queued, waiting for the printer actor.
	JobPending JobState = "pending"
	// JobTransforming: running the WASM transform pipeline.
	JobTransforming JobState = "transforming"
	// JobSpooled: transformed bytes are in the spool, ready to send.
	JobSpooled JobState = "spooled"
	// JobSending: streaming to the printer over IPP.
	JobSending JobState = "sending"
	// JobCompleted: the printer accepted the document. Terminal.
	JobCompleted JobState = "completed"
	// JobCanceled: cancelled by a user. Terminal.
	JobCanceled JobState = "canceled"
	// JobAborted: failed and not retryable. Terminal.
	JobAborted JobState = "aborted"
	// JobHeld: paused, awaiting release.
	JobHeld JobState = "held"
)

// JobFSMTag is the go-foundation FSM definition for a job. The queue actor
// drives transitions; the machine enforces that only legal moves happen and
// records the history. sending->pending exists for restart recovery (an
// in-flight send is requeued and reconciled). *->canceled lets a user cancel
// from any non-terminal state.
// pending->spooled is the fast path: when the printer accepts the document
// format directly the transform plan is empty, so the job skips the
// transforming state entirely (the doc passes verbatim). pending->transforming
// is the path taken when a plan exists.
const JobFSMTag = "initial:created; " +
	"created->validating; " +
	"validating->pending; validating->aborted; " +
	"pending->transforming; pending->spooled; pending->held; pending->aborted; " +
	"held->pending; " +
	"transforming->spooled; transforming->aborted; " +
	"spooled->sending; " +
	"sending->completed; sending->pending; sending->aborted; " +
	"*->canceled"

// IsTerminal reports whether no further transitions are expected.
func (s JobState) IsTerminal() bool {
	switch s {
	case JobCompleted, JobCanceled, JobAborted:
		return true
	default:
		return false
	}
}

// JobTicket is the set of print options for a job, close to the IPP job
// template. Zero-valued fields mean "use the printer default".
type JobTicket struct {
	Copies int
	Sides  string // one-sided, two-sided-long-edge, two-sided-short-edge
	Media  string // e.g. iso_a4_210x297mm
	Pages  string // page range, e.g. "1-4,7"
	Color  string // print-color-mode: monochrome, color, auto
}

// DocRef points at a content-addressed blob in the spool.
type DocRef struct {
	SHA256 string // hex digest, the spool key
	Format MIME   // sniffed, never trusted from the client
	Size   int64
}

// Job is a unit of work: an FSM plus references to its documents and a ticket.
// The State field is mutated by the go-foundation FSM (via reflection), so it
// carries the FSM tag and must stay a plain string.
type Job struct {
	ID        ID
	PrinterID ID
	Name      string
	State     string `fsm:"initial:created; created->validating; validating->pending; validating->aborted; pending->transforming; pending->spooled; pending->held; pending->aborted; held->pending; transforming->spooled; transforming->aborted; spooled->sending; sending->completed; sending->pending; sending->aborted; *->canceled"`

	Ticket   JobTicket
	Document DocRef

	// RemoteJobID is the job-id the printer assigned, used to reconcile state
	// with Get-Job-Attributes after a restart.
	RemoteJobID int
	// Error holds the failure cause when State is aborted.
	Error string

	Created time.Time
	Updated time.Time
}

// EventKind classifies a job event in the append-only log.
type EventKind string

const (
	// EventTransition is a state change.
	EventTransition EventKind = "transition"
	// EventNote is a non-transition annotation (e.g. a retry attempt).
	EventNote EventKind = "note"
)

// Event is one append-only entry in a job's history. This is the light event
// sourcing that lets `torchio queue history <job>` explain WHY a job died, not
// just that it did.
type Event struct {
	JobID ID
	Seq   int64 // monotonic within a job
	At    time.Time
	Kind  EventKind
	From  JobState // for transitions
	To    JobState // for transitions
	Actor string   // "user", "spooler", "printer"
	Cause string   // human-readable reason
}
