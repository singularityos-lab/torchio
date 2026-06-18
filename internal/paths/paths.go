// Package paths resolves the per-user locations Torchio uses, following the XDG
// base directory spec. State (the SQLite database and the spool) lives under
// XDG_STATE_HOME; the control socket lives under XDG_RUNTIME_DIR.
package paths

import (
	"os"
	"path/filepath"
)

// appDir is the per-user subdirectory name across XDG roots.
const appDir = "torchio"

// StateDir returns the directory for mutable state (database, spool), creating
// it if needed.
func StateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, appDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// DatabasePath returns the SQLite database path.
func DatabasePath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.db"), nil
}

// SpoolDir returns the content-addressed spool directory.
func SpoolDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spool"), nil
}

// ConfigDir returns the per-user configuration directory (XDG_CONFIG_HOME). It
// is not created: the daemon never writes configuration, and a missing
// directory means defaults.
func ConfigDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appDir), nil
}

// TrustPolicyPath returns the default path of the declarative trust policy file,
// used by the "policy" trust backend when no explicit path is configured.
func TrustPolicyPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "trust.policy"), nil
}

// SocketPath returns the control Unix-domain-socket path under the runtime dir.
// It falls back to the state dir when XDG_RUNTIME_DIR is unset (e.g. headless).
func SocketPath() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		// No runtime dir (e.g. headless): keep the socket beside the state.
		dir, err := StateDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "control.sock"), nil
	}
	dir := filepath.Join(base, appDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "control.sock"), nil
}
