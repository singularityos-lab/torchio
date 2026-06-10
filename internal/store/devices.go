package store

import "time"

// SeenDevice is one entry in the ephemeral discovery cache.
type SeenDevice struct {
	Key      string // mDNS service instance name
	URI      string
	Name     string
	LastSeen time.Time
}

// RecordDevice upserts a discovered device into the read-only cache. Discovery
// NEVER creates a printer; it only fills this table. A printer is born only
// when a user confirms one through the portal (model.OriginDiscoveredConfirmed).
func (s *Store) RecordDevice(d SeenDevice) error {
	_, err := s.db.Exec(`
		INSERT INTO devices_seen (key, uri, name, last_seen_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			uri=excluded.uri, name=excluded.name, last_seen_ms=excluded.last_seen_ms`,
		d.Key, d.URI, d.Name, toMs(d.LastSeen))
	return err
}

// ListDevices returns cached devices seen since the given cutoff (zero cutoff
// returns all). Used to populate the "found on the network" section of a print
// dialog.
func (s *Store) ListDevices(since time.Time) ([]SeenDevice, error) {
	rows, err := s.db.Query(`
		SELECT key, uri, name, last_seen_ms FROM devices_seen
		WHERE last_seen_ms >= ? ORDER BY name`, toMs(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeenDevice
	for rows.Next() {
		var d SeenDevice
		var ms int64
		if err := rows.Scan(&d.Key, &d.URI, &d.Name, &ms); err != nil {
			return nil, err
		}
		d.LastSeen = fromMs(ms)
		out = append(out, d)
	}
	return out, rows.Err()
}

// PruneDevices drops cache entries older than cutoff, keeping the ephemeral
// table from growing without bound.
func (s *Store) PruneDevices(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM devices_seen WHERE last_seen_ms < ?`, toMs(cutoff))
	return err
}
