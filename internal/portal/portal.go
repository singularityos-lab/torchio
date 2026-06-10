// Package portal is Torchio's bridge to the ush permission broker. Principle
// P4: Torchio implements no consent UI of its own and uses no PolicyKit. Every
// trust decision (adding a discovered printer, trusting a TLS certificate,
// using a USB device, sharing a queue) is routed to the ush broker, which
// derives the smallest scope and asks the user through the desktop portal.
//
// The Portal interface keeps that dependency injectable: the daemon uses the
// D-Bus broker, tests use a deterministic fake, and a headless run can fall
// back to a policy that never silently grants.
package portal

import "context"

// Decision is the outcome of a permission request, matching the four strings
// the ush portal returns.
type Decision string

const (
	// Allow grants this time only.
	Allow Decision = "allow"
	// AllowSession grants for the lifetime of the session.
	AllowSession Decision = "allow_session"
	// AllowAlways grants and persists the decision.
	AllowAlways Decision = "allow_always"
	// Deny rejects the request.
	Deny Decision = "deny"
)

// Granted reports whether the decision permits the action.
func (d Decision) Granted() bool {
	switch d {
	case Allow, AllowSession, AllowAlways:
		return true
	default:
		return false
	}
}

// Permission categories, matching the ush broker vocabulary.
const (
	CategoryNetwork = "network"
	CategoryDevice  = "device"
	CategoryFilesys = "filesystem"
	CategoryDBus    = "dbus.sensitive"
)

// Portal asks the user (via ush) to approve a single permission. The broker
// keeps the raw target in its audit log; resource is the suggested grant scope.
type Portal interface {
	RequestPermission(ctx context.Context, category, resource, reason string) (Decision, error)
}

// AutoApprove grants everything. It is for headless development and tests only;
// the daemon must never use it when a real user could be asked.
type AutoApprove struct{}

// RequestPermission always allows.
func (AutoApprove) RequestPermission(context.Context, string, string, string) (Decision, error) {
	return Allow, nil
}

// AlwaysDeny refuses everything. It is the safe fallback when no portal is
// reachable: a printer is never added, a cert never trusted, without a human.
type AlwaysDeny struct{}

// RequestPermission always denies.
func (AlwaysDeny) RequestPermission(context.Context, string, string, string) (Decision, error) {
	return Deny, nil
}
