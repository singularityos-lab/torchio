package portal

import (
	"bufio"
	"context"
	"os"
	"strings"
)

// PolicyRule is one declarative trust rule. If Category matches the request
// (or is "*") and the resource matches the glob Pattern, Decide is returned.
type PolicyRule struct {
	Category string
	Pattern  string
	Decide   Decision // Allow or Deny
}

// PolicyPortal answers from a static rule list with no prompting. It is the
// backend for headless or non-interactive deployments and for distributions
// that prefer declarative trust to an interactive broker: a site ships a policy
// that, for example, auto-approves printers on its own network and denies the
// rest. The first matching rule wins; Default applies when none match.
type PolicyPortal struct {
	Rules   []PolicyRule
	Default Decision
}

// RequestPermission evaluates the rules in order.
func (p *PolicyPortal) RequestPermission(_ context.Context, category, resource, _ string) (Decision, error) {
	for _, r := range p.Rules {
		if (r.Category == "*" || r.Category == category) && globMatch(r.Pattern, resource) {
			return r.Decide, nil
		}
	}
	if p.Default == "" {
		return Deny, nil
	}
	return p.Default, nil
}

// LoadPolicy reads a policy file. Each non-comment line is either
//
//	default <allow|deny>
//
// or
//
//	<category|*> <resource-glob> <allow|deny>
//
// where '*' in the glob matches any run of characters. A missing file yields a
// deny-by-default policy, which is the safe fallback (a printer is never added
// without an explicit rule).
func LoadPolicy(path string) (*PolicyPortal, error) {
	pp := &PolicyPortal{Default: Deny}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pp, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) == 2 && fields[0] == "default":
			pp.Default = parseDecision(fields[1])
		case len(fields) == 3:
			pp.Rules = append(pp.Rules, PolicyRule{
				Category: fields[0], Pattern: fields[1], Decide: parseDecision(fields[2]),
			})
		}
	}
	return pp, sc.Err()
}

func parseDecision(s string) Decision {
	if strings.EqualFold(s, "allow") {
		return Allow
	}
	return Deny
}

// globMatch reports whether s matches pattern, where '*' is any run of
// characters (the only metacharacter). It avoids filepath.Match because the
// resources are URIs whose '/' must not be treated specially.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		idx := strings.Index(s, mid)
		if idx < 0 {
			return false
		}
		s = s[idx+len(mid):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
