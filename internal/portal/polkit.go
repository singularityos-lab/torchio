package portal

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
)

// PolkitPortal routes trust decisions to PolicyKit, the standard Linux
// authorization service, over the system bus. It is the portability backend for
// distributions outside the Singularity ecosystem that already run polkit: each
// permission category maps to a polkit action id (org.torchio.trust.<category>),
// and the distribution ships a matching .policy file describing those actions
// and their defaults. It is selected only when explicitly configured.
type PolkitPortal struct {
	conn      *dbus.Conn
	startTime uint64
}

// NewPolkit connects to the system bus where the PolicyKit authority lives.
func NewPolkit() (*PolkitPortal, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("portal: connect system bus: %w", err)
	}
	return &PolkitPortal{conn: conn, startTime: selfStartTime()}, nil
}

// Close releases the bus connection.
func (p *PolkitPortal) Close() error {
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

// RequestPermission calls CheckAuthorization with AllowUserInteraction set, so
// PolicyKit may invoke its authentication agent and block until the user
// decides. An authorized result is Allow; anything else is Deny.
func (p *PolkitPortal) RequestPermission(ctx context.Context, category, resource, reason string) (Decision, error) {
	subject := struct {
		Kind    string
		Details map[string]dbus.Variant
	}{
		Kind: "unix-process",
		Details: map[string]dbus.Variant{
			"pid":        dbus.MakeVariant(uint32(os.Getpid())),
			"start-time": dbus.MakeVariant(p.startTime),
		},
	}
	action := "org.torchio.trust." + category
	details := map[string]string{"resource": resource, "reason": reason}
	const allowUserInteraction = uint32(1)

	var res struct {
		IsAuthorized bool
		IsChallenge  bool
		Details      map[string]string
	}
	obj := p.conn.Object("org.freedesktop.PolicyKit1", "/org/freedesktop/PolicyKit1/Authority")
	call := obj.CallWithContext(ctx, "org.freedesktop.PolicyKit1.Authority.CheckAuthorization", 0,
		subject, action, details, allowUserInteraction, "")
	if call.Err != nil {
		return Deny, fmt.Errorf("portal: polkit CheckAuthorization: %w", call.Err)
	}
	if err := call.Store(&res); err != nil {
		return Deny, fmt.Errorf("portal: decode polkit result: %w", err)
	}
	if res.IsAuthorized {
		return Allow, nil
	}
	return Deny, nil
}

// selfStartTime reads this process's start time (clock ticks since boot) from
// field 22 of /proc/self/stat, which PolicyKit uses to pin the subject against
// PID reuse. Zero is acceptable when it cannot be read.
func selfStartTime() uint64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	s := string(data)
	// The comm field (2) is parenthesized and may contain spaces, so start
	// parsing after the last ')'. The next token is field 3, hence starttime
	// (field 22) is index 19 in the remainder.
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 20 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[19], 10, 64)
	return v
}
