package portal

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// ush broker D-Bus addressing.
const (
	brokerName  = "io.github.mirkobrombin.ush.Broker"
	brokerPath  = "/io/github/mirkobrombin/ush/Broker"
	brokerIface = "io.github.mirkobrombin.ush.Broker1"
)

// DBusPortal calls the ush broker over the session bus. The broker derives the
// smallest useful scope from the request and shows it to the user through the
// desktop's portal, then returns one of the four decisions.
type DBusPortal struct {
	conn      *dbus.Conn
	sessionID string
}

// NewDBus connects to the session bus. The caller closes it with Close. It
// returns an error if no session bus is available (e.g. a headless run), so the
// daemon can choose a fallback policy.
func NewDBus(sessionID string) (*DBusPortal, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("portal: connect session bus: %w", err)
	}
	return &DBusPortal{conn: conn, sessionID: sessionID}, nil
}

// Close releases the bus connection.
func (p *DBusPortal) Close() error {
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

// RequestPermission calls Broker1.RequestPermission and returns the decision.
func (p *DBusPortal) RequestPermission(ctx context.Context, category, resource, reason string) (Decision, error) {
	obj := p.conn.Object(brokerName, dbus.ObjectPath(brokerPath))
	var decision string
	call := obj.CallWithContext(ctx, brokerIface+".RequestPermission", 0,
		category, resource, reason, p.sessionID)
	if call.Err != nil {
		return Deny, fmt.Errorf("portal: RequestPermission: %w", call.Err)
	}
	if err := call.Store(&decision); err != nil {
		return Deny, fmt.Errorf("portal: decode decision: %w", err)
	}
	return Decision(decision), nil
}
