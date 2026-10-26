package portal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// BrokerPortal asks the ush broker over its private control socket. The broker
// derives the smallest useful scope from the request, shows it to the user
// through the desktop's portal, remembers the answer, and returns one of the
// four decisions.
type BrokerPortal struct {
	socket    string
	sessionID string
}

type brokerRequest struct {
	Method    string `json:"method"`
	Category  string `json:"category"`
	Resource  string `json:"resource"`
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session,omitempty"`
}

type brokerResponse struct {
	Decision string `json:"decision"`
	Error    string `json:"error"`
}

// brokerSocket is $XDG_RUNTIME_DIR/ush/broker.sock, or USH_BROKER_SOCK when set.
func brokerSocket() string {
	if s := os.Getenv("USH_BROKER_SOCK"); s != "" {
		return s
	}
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		rt = filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(rt, "ush", "broker.sock")
}

// NewBroker returns a portal backed by the ush broker. It fails when the broker
// socket does not exist, so the daemon can choose a fallback policy.
func NewBroker(sessionID string) (*BrokerPortal, error) {
	socket := brokerSocket()
	if _, err := os.Stat(socket); err != nil {
		return nil, fmt.Errorf("portal: ush broker: %w", err)
	}
	return &BrokerPortal{socket: socket, sessionID: sessionID}, nil
}

// Close is a no-op: every request uses its own connection.
func (p *BrokerPortal) Close() error {
	return nil
}

// RequestPermission sends one RequestPermission call and returns the decision.
// The user may take a while to answer, so the deadline comes from ctx alone.
func (p *BrokerPortal) RequestPermission(ctx context.Context, category, resource, reason string) (Decision, error) {
	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	conn, err := d.DialContext(dialCtx, "unix", p.socket)
	cancel()
	if err != nil {
		return Deny, fmt.Errorf("portal: RequestPermission: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	req, err := json.Marshal(brokerRequest{
		Method:    "RequestPermission",
		Category:  category,
		Resource:  resource,
		Reason:    reason,
		SessionID: p.sessionID,
	})
	if err != nil {
		return Deny, err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return Deny, fmt.Errorf("portal: RequestPermission: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Deny, fmt.Errorf("portal: RequestPermission: %w", err)
	}
	var resp brokerResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return Deny, fmt.Errorf("portal: decode decision: %w", err)
	}
	if resp.Error != "" {
		return Deny, fmt.Errorf("portal: RequestPermission: %s", resp.Error)
	}
	return Decision(resp.Decision), nil
}
