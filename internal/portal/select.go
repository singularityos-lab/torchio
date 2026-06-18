package portal

import "fmt"

// Trust backend names accepted by Select.
const (
	// BackendUSH delegates to the ush broker over D-Bus. This is the default
	// and the only backend used on the Singularity ecosystem (principle P4).
	BackendUSH = "ush"
	// BackendPolkit delegates to PolicyKit. It exists so distributions outside
	// the Singularity ecosystem can adopt Torchio against the authorization
	// service they already run; it is never selected on Singularity.
	BackendPolkit = "polkit"
	// BackendPolicy answers from a declarative policy file, with no prompting.
	// Suited to headless or non-interactive deployments.
	BackendPolicy = "policy"
	// BackendDeny refuses everything: the safe default when nothing else fits.
	BackendDeny = "deny"
	// BackendAllow grants everything. Development only; never for real use.
	BackendAllow = "allow"
)

// Select builds the trust backend named by backend. This factory is the seam
// that makes Torchio portable: the core depends only on the Portal interface,
// so the ush broker is the Singularity default while any other distribution can
// pick polkit, a declarative policy file, or deny-by-default without the core
// knowing which is in use. An unknown name is an error so a misconfiguration
// fails loudly rather than silently granting or denying.
func Select(backend, sessionID, policyFile string) (Portal, error) {
	switch backend {
	case "", BackendUSH:
		return NewDBus(sessionID)
	case BackendPolkit:
		return NewPolkit()
	case BackendPolicy:
		return LoadPolicy(policyFile)
	case BackendDeny:
		return AlwaysDeny{}, nil
	case BackendAllow:
		return AutoApprove{}, nil
	default:
		return nil, fmt.Errorf("portal: unknown trust backend %q", backend)
	}
}
