package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/singularityos-lab/torchio/internal/model"
)

// newStore opens a file-backed store in a temp dir. We avoid :memory: for most
// tests so the WAL/foreign-key path is exercised exactly as in production.
func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func samplePrinter() *model.Printer {
	now := time.Now().Truncate(time.Millisecond)
	return &model.Printer{
		ID:      model.NewID(),
		Name:    "office",
		URI:     "ipps://office.local:631/ipp/print",
		Origin:  model.OriginManual,
		Created: now,
		Updated: now,
		Caps: model.Capabilities{
			MakeAndModel:    "Torchio Test Laser",
			DocumentFormats: []model.MIME{model.MIMEPDF, model.MIMEPWGRaster},
			Media:           []string{"iso_a4_210x297mm"},
			Sides:           []string{"one-sided", "two-sided-long-edge"},
		},
	}
}

func TestPrinterCRUD(t *testing.T) {
	s := newStore(t)
	p := samplePrinter()
	if err := s.SavePrinter(p); err != nil {
		t.Fatalf("SavePrinter: %v", err)
	}

	got, err := s.GetPrinter(p.ID)
	if err != nil {
		t.Fatalf("GetPrinter: %v", err)
	}
	if got.Name != "office" || got.URI != p.URI {
		t.Errorf("printer mismatch: %+v", got)
	}
	if len(got.Caps.DocumentFormats) != 2 || !got.Caps.AcceptsFormat(model.MIMEPDF) {
		t.Errorf("caps not round-tripped: %+v", got.Caps)
	}

	byName, err := s.GetPrinterByName("office")
	if err != nil || byName.ID != p.ID {
		t.Fatalf("GetPrinterByName: %v, %+v", err, byName)
	}

	// Update in place.
	p.URI = "ipps://office.local:631/ipp/print2"
	p.Updated = p.Updated.Add(time.Second)
	if err := s.SavePrinter(p); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetPrinter(p.ID)
	if got.URI != p.URI {
		t.Errorf("update not persisted: %s", got.URI)
	}

	if err := s.DeletePrinter(p.ID); err != nil {
		t.Fatalf("DeletePrinter: %v", err)
	}
	if _, err := s.GetPrinter(p.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDefaultPrinter(t *testing.T) {
	s := newStore(t)
	p1, p2 := samplePrinter(), samplePrinter()
	p2.Name = "home"
	if err := s.SavePrinter(p1); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePrinter(p2); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DefaultPrinter(); err != ErrNotFound {
		t.Errorf("expected no default initially, got %v", err)
	}
	if err := s.SetDefaultPrinter(p1.ID); err != nil {
		t.Fatal(err)
	}
	def, err := s.DefaultPrinter()
	if err != nil || def.ID != p1.ID {
		t.Fatalf("default = %+v, %v", def, err)
	}
	// Switching default must clear the previous one.
	if err := s.SetDefaultPrinter(p2.ID); err != nil {
		t.Fatal(err)
	}
	def, _ = s.DefaultPrinter()
	if def.ID != p2.ID {
		t.Errorf("default not switched: %s", def.Name)
	}
	all, _ := s.ListPrinters()
	defaults := 0
	for _, p := range all {
		if p.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly 1 default, got %d", defaults)
	}
}

func TestJobLifecycleAndEvents(t *testing.T) {
	s := newStore(t)
	p := samplePrinter()
	if err := s.SavePrinter(p); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Millisecond)
	j := &model.Job{
		ID:        model.NewID(),
		PrinterID: p.ID,
		Name:      "report.pdf",
		State:     string(model.JobCreated),
		Ticket:    model.JobTicket{Copies: 2, Sides: "two-sided-long-edge"},
		Document:  model.DocRef{SHA256: "abc123", Format: model.MIMEPDF, Size: 4096},
		Created:   now,
		Updated:   now,
	}
	if err := s.SaveJob(j); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}

	got, err := s.GetJob(j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Ticket.Copies != 2 || got.Document.SHA256 != "abc123" {
		t.Errorf("job not round-tripped: %+v", got)
	}

	// Transition and persist.
	j.State = string(model.JobPending)
	j.Updated = now.Add(time.Second)
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}

	// Append a couple of events; sequence numbers must auto-increment.
	for _, ev := range []*model.Event{
		{JobID: j.ID, At: now, Kind: model.EventTransition, From: model.JobCreated, To: model.JobValidating, Actor: "spooler"},
		{JobID: j.ID, At: now.Add(time.Second), Kind: model.EventTransition, From: model.JobValidating, To: model.JobPending, Actor: "spooler"},
	} {
		if err := s.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	events, err := s.JobEvents(j.ID)
	if err != nil {
		t.Fatalf("JobEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Errorf("sequence numbers = %d,%d, want 1,2", events[0].Seq, events[1].Seq)
	}
	if events[1].To != model.JobPending {
		t.Errorf("last event to = %s", events[1].To)
	}

	// Filters.
	active, _ := s.ListJobs(JobFilter{PrinterID: p.ID, Active: true})
	if len(active) != 1 {
		t.Errorf("expected 1 active job, got %d", len(active))
	}
	refs, _ := s.ReferencedDigests()
	if _, ok := refs["abc123"]; !ok {
		t.Errorf("digest abc123 should be referenced")
	}
}

func TestForeignKeyCascade(t *testing.T) {
	s := newStore(t)
	p := samplePrinter()
	if err := s.SavePrinter(p); err != nil {
		t.Fatal(err)
	}
	j := &model.Job{ID: model.NewID(), PrinterID: p.ID, State: string(model.JobCreated), Created: time.Now(), Updated: time.Now()}
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(&model.Event{JobID: j.ID, At: time.Now(), Kind: model.EventNote, Cause: "test"}); err != nil {
		t.Fatal(err)
	}

	// Deleting the printer must cascade to its jobs and their events.
	if err := s.DeletePrinter(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetJob(j.ID); err != ErrNotFound {
		t.Errorf("job should be cascade-deleted, got %v", err)
	}
	events, _ := s.JobEvents(j.ID)
	if len(events) != 0 {
		t.Errorf("events should be cascade-deleted, got %d", len(events))
	}
}

func TestDevicesCache(t *testing.T) {
	s := newStore(t)
	now := time.Now().Truncate(time.Millisecond)
	if err := s.RecordDevice(SeenDevice{Key: "HP._ipp._tcp", URI: "ipps://hp.local/ipp/print", Name: "HP LaserJet", LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	// Upsert same key updates rather than duplicates.
	if err := s.RecordDevice(SeenDevice{Key: "HP._ipp._tcp", URI: "ipps://hp.local/ipp/print", Name: "HP LaserJet Pro", LastSeen: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	devices, err := s.ListDevices(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Name != "HP LaserJet Pro" {
		t.Errorf("devices = %+v", devices)
	}
}
