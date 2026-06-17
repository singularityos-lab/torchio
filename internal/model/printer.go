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

// Printer is a print destination. By principle P1 it is always an IPP endpoint
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

	Created time.Time
	Updated time.Time
}
