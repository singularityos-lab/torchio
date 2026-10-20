package model

import "time"

// JobState is the lifecycle state of a job. The values map 1:1 onto IPP
// "job-state" so that sharing a queue is a projection, not a translation.
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

// transitions lists the legal moves of the job FSM. sending->pending exists
// for restart recovery: an in-flight send is requeued and reconciled with
// Get-Job-Attributes. pending->spooled is the passthrough case, where the
// printer accepts the format and there is nothing to transform. Cancellation
// is allowed from every non-terminal state and is handled in CanTransition.
var transitions = map[JobState][]JobState{
	JobCreated:      {JobValidating},
	JobValidating:   {JobPending, JobAborted},
	JobPending:      {JobTransforming, JobSpooled, JobHeld, JobAborted},
	JobSpooled:      {JobSending, JobHeld},
	JobHeld:         {JobPending},
	JobCanceled:     {JobPending},
	JobAborted:      {JobPending},
	JobTransforming: {JobSpooled, JobAborted},
	JobSending:      {JobCompleted, JobPending, JobAborted},
}

// CanTransition reports whether a job may move from one state to another.
//
// Restarting a canceled or aborted job moves it back to pending.
func CanTransition(from, to JobState) bool {
	if to == JobCanceled {
		return !from.IsTerminal()
	}
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

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
	// Quality is the IPP print-quality enum: 3 draft, 4 normal, 5 high.
	Quality int
	// Hold keeps the job from printing until it is released.
	Hold bool
}

// DocRef points at a content-addressed blob in the spool.
type DocRef struct {
	SHA256 string // hex digest, the spool key
	Format MIME   // sniffed, never trusted from the client
	Size   int64
}

// Job is a unit of work: an FSM plus references to its documents and a ticket.
// State only changes through CanTransition-checked moves in the queue engine.
type Job struct {
	ID ID
	// Number is the small integer job-id shown to CUPS clients, assigned by
	// the store on first save.
	Number    int
	PrinterID ID
	Name      string
	State     string

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
