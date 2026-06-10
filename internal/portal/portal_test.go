package portal

import (
	"context"
	"testing"
)

func TestDecisionGranted(t *testing.T) {
	for _, d := range []Decision{Allow, AllowSession, AllowAlways} {
		if !d.Granted() {
			t.Errorf("%s should be granted", d)
		}
	}
	for _, d := range []Decision{Deny, "", "bogus"} {
		if d.Granted() {
			t.Errorf("%q should not be granted", d)
		}
	}
}

func TestFakePortals(t *testing.T) {
	ctx := context.Background()
	if d, _ := (AutoApprove{}).RequestPermission(ctx, CategoryNetwork, "ipps://x", "why"); d != Allow {
		t.Errorf("AutoApprove returned %s", d)
	}
	if d, _ := (AlwaysDeny{}).RequestPermission(ctx, CategoryNetwork, "ipps://x", "why"); d.Granted() {
		t.Errorf("AlwaysDeny granted %s", d)
	}
}
