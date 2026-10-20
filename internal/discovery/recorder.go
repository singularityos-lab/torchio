package discovery

import (
	"context"
	"log"
	"time"

	"github.com/singularityos-lab/torchio/internal/store"
)

// Recorder consumes services from a Browser and writes them to the read-only
// devices_seen cache. It is the only thing discovery does: record. It never
// creates a printer. Periodically it prunes stale entries so the cache reflects
// what is currently on the network.
type Recorder struct {
	Store      *store.Store
	Log        *log.Logger
	TTL        time.Duration // entries older than this are pruned (default 5m)
	PruneEvery time.Duration
}

// Run browses and records until ctx is cancelled. It returns the browser's
// start error, if any; otherwise it blocks.
func (r Recorder) Run(ctx context.Context, b Browser, now func() time.Time) error {
	ttl := r.TTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	pruneEvery := r.PruneEvery
	if pruneEvery == 0 {
		pruneEvery = ttl
	}

	ch, err := b.Browse(ctx)
	if err != nil {
		return err
	}

	prune := time.NewTicker(pruneEvery)
	defer prune.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case s, ok := <-ch:
			if !ok {
				return nil
			}
			r.record(s, now())
		case <-prune.C:
			if err := r.Store.PruneDevices(now().Add(-ttl)); err != nil && r.Log != nil {
				r.Log.Printf("discovery prune failed: %v", err)
			}
		}
	}
}

// record upserts one discovered service into the cache. This is a read-only
// operation with respect to the printer configuration: it touches only
// devices_seen, never the printers table.
func (r Recorder) record(s Service, now time.Time) {
	dev := store.SeenDevice{
		Key:      s.Key(),
		URI:      s.URI(),
		Name:     s.Model(),
		LastSeen: now,
	}
	if err := r.Store.RecordDevice(dev); err != nil && r.Log != nil {
		r.Log.Printf("discovery record failed: %v", err)
	}
}
