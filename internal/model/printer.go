package model

import "time"

// Origin records how a printer entered the configuration. The distinction
// matters for security: a printer is only ever created from discovery after an
// explicit human confirmation through the portal, never by a network packet
// (this is the lesson of the cups-browsed CVEs).
type Origin string

const (
	// OriginManual means a user added the printer by URI.
	OriginManual Origin = "manual"
	// OriginDiscoveredConfirmed means discovery found it and the user
	// confirmed it through the portal.
	OriginDiscoveredConfirmed Origin = "discovered+confirmed"
)

// Printer is a print destination. It is always an IPP endpoint
// identified by a URI; there is no notion of a driver here.
type Printer struct {
	ID      ID
	Name    string // user-chosen name
	URI     string // ipps://... or ipp://localhost:PORT for a host driver
	Origin  Origin
	Default bool

	// Caps is the cached capability snapshot, refreshed periodically from the
	// printer's IPP attributes.
	Caps Capabilities

	// RawAttrs is the encoded Get-Printer-Attributes response captured when the
	// printer was added: the printer's full IPP attribute set. The CUPS-compat
	// front end replays it (with the queue-specific attributes overridden) so
	// libcups clients see exactly what the real printer advertises.
	RawAttrs []byte

	Queue QueueSettings

	Created time.Time
	Updated time.Time
}

// QueueSettings is what a user configures on a queue, as opposed to what the
// printer reports about itself.
type QueueSettings struct {
	Info     string
	Location string
	// Paused keeps jobs queued without sending them.
	Paused bool
	// Message says why the queue is paused.
	Message string
	// Rejecting refuses new jobs.
	Rejecting bool
	Shared    bool
	// Defaults fills ticket fields a job leaves empty.
	Defaults JobTicket
}

// Ticket returns t with the queue defaults applied to its empty fields.
func (q QueueSettings) Ticket(t JobTicket) JobTicket {
	d := q.Defaults
	if t.Copies == 0 {
		t.Copies = d.Copies
	}
	if t.Sides == "" {
		t.Sides = d.Sides
	}
	if t.Media == "" {
		t.Media = d.Media
	}
	if t.Color == "" {
		t.Color = d.Color
	}
	if t.Quality == 0 {
		t.Quality = d.Quality
	}
	return t
}
