package model

import (
	"sort"
	"testing"
	"time"
)

func TestIDFormat(t *testing.T) {
	id := NewID()
	s := id.String()
	if len(s) != 26 {
		t.Fatalf("ID string length = %d, want 26 (%q)", len(s), s)
	}
	for _, c := range s {
		if !isCrockford(byte(c)) {
			t.Fatalf("ID %q contains non-Crockford char %q", s, c)
		}
	}
}

func TestIDTimeRoundTrip(t *testing.T) {
	when := time.UnixMilli(1_700_000_000_123)
	id := newIDAt(when)
	got := id.Time()
	if !got.Equal(when) {
		t.Errorf("ID.Time() = %v, want %v", got, when)
	}
}

// TestIDSortable verifies that IDs minted in time order sort in that same
// order by their string form, which is what lets the store list jobs in
// creation order off the primary key.
func TestIDSortable(t *testing.T) {
	base := time.UnixMilli(1_700_000_000_000)
	var ids []string
	for i := 0; i < 50; i++ {
		ids = append(ids, newIDAt(base.Add(time.Duration(i)*time.Millisecond)).String())
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("IDs not lexicographically time-sorted at %d:\n got %v\nwant %v", i, ids, sorted)
		}
	}
}

func TestParseIDRoundTrip(t *testing.T) {
	for i := 0; i < 1000; i++ {
		id := NewID()
		s := id.String()
		got, err := ParseID(s)
		if err != nil {
			t.Fatalf("ParseID(%q): %v", s, err)
		}
		if got != id {
			t.Fatalf("round trip mismatch: %x -> %q -> %x", id, s, got)
		}
	}
}

func TestParseIDErrors(t *testing.T) {
	for _, bad := range []string{"", "too-short", "0000000000000000000000000I", "!!!!!!!!!!!!!!!!!!!!!!!!!!"} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) expected error", bad)
		}
	}
}

func TestIDUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 10000; i++ {
		s := NewID().String()
		if seen[s] {
			t.Fatalf("duplicate ID generated: %q", s)
		}
		seen[s] = true
	}
}

func TestJobStateTerminal(t *testing.T) {
	terminal := []JobState{JobCompleted, JobCanceled, JobAborted}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []JobState{JobCreated, JobPending, JobSending, JobHeld} {
		if s.IsTerminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func isCrockford(c byte) bool {
	for i := 0; i < len(crockford); i++ {
		if crockford[i] == c {
			return true
		}
	}
	return false
}
