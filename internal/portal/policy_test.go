package portal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "anything", true},
		{"ipp://10.30.10.33/ipp/print", "ipp://10.30.10.33/ipp/print", true},
		{"ipp://10.30.10.*", "ipp://10.30.10.33/ipp/print", true},
		{"ipp://10.30.10.*", "ipp://10.30.20.5/ipp/print", false},
		{"*/ipp/print", "ipp://host/ipp/print", true},
		{"ipp://*/ipp/print", "ipp://host/ipp/print", true},
		{"ipp://*/ipp/print", "ipp://host/eSCL", false},
		{"exact", "different", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q)=%v want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestPolicyEval(t *testing.T) {
	p := &PolicyPortal{
		Default: Deny,
		Rules: []PolicyRule{
			{Category: "network", Pattern: "ipp://10.30.10.*", Decide: Allow},
			{Category: "device", Pattern: "*", Decide: Deny},
		},
	}
	check := func(cat, res string, want Decision) {
		got, err := p.RequestPermission(context.Background(), cat, res, "")
		if err != nil || got != want {
			t.Errorf("%s %s -> %v (err %v), want %v", cat, res, got, err, want)
		}
	}
	check("network", "ipp://10.30.10.33/ipp/print", Allow)
	check("network", "ipp://8.8.8.8/ipp/print", Deny) // no rule -> default deny
	check("device", "usb://anything", Deny)
	check("filesystem", "/etc/passwd", Deny) // no rule -> default deny
}

func TestLoadPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.policy")
	content := "# comment\ndefault deny\nnetwork ipp://10.30.10.* allow\ndevice * deny\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Default != Deny || len(p.Rules) != 2 {
		t.Fatalf("parsed default=%q rules=%d", p.Default, len(p.Rules))
	}
	d, _ := p.RequestPermission(context.Background(), "network", "ipp://10.30.10.5/ipp/print", "")
	if d != Allow {
		t.Errorf("expected Allow for LAN printer, got %v", d)
	}
}

func TestLoadPolicyMissingFileDenies(t *testing.T) {
	p, err := LoadPolicy(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if d, _ := p.RequestPermission(context.Background(), "network", "x", ""); d != Deny {
		t.Errorf("missing policy must deny by default, got %v", d)
	}
}

func TestSelect(t *testing.T) {
	if p, err := Select(BackendDeny, "", ""); err != nil || p == nil {
		t.Errorf("deny backend: %v", err)
	}
	if p, err := Select(BackendAllow, "", ""); err != nil || p == nil {
		t.Errorf("allow backend: %v", err)
	}
	if _, err := Select("nonsense", "", ""); err == nil {
		t.Error("unknown backend should error")
	}
	p, err := Select(BackendPolicy, "", filepath.Join(t.TempDir(), "none"))
	if err != nil {
		t.Fatalf("policy backend: %v", err)
	}
	if d, _ := p.RequestPermission(context.Background(), "network", "x", ""); d != Deny {
		t.Errorf("empty policy denies, got %v", d)
	}
}
