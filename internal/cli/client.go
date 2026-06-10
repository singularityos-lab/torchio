package cli

import (
	"github.com/singularityos-lab/torchio/internal/control"
	"github.com/singularityos-lab/torchio/internal/paths"
)

// dialDaemon connects to the running torchiod over its control socket. Commands
// that manage queues and printers go through the daemon so all state lives in
// one place; commands that only touch a printer directly (print,
// probe) do not need it.
func dialDaemon() (*control.Client, error) {
	socket, err := paths.SocketPath()
	if err != nil {
		return nil, err
	}
	return control.Dial(socket)
}
