package store

import (
	"fmt"

	"github.com/singularityos-lab/torchio/internal/model"
)

// AppendEvent writes one entry to a job's append-only history. The sequence
// number is assigned here as max(seq)+1 for the job, so callers do not race on
// it.
func (s *Store) AppendEvent(e *model.Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := appendEvent(tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

func appendEvent(tx execer, e *model.Event) error {
	var next int64
	// COALESCE handles the first event for a job (no rows yet).
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE job_id = ?`,
		e.JobID.String()).Scan(&next); err != nil {
		return err
	}
	e.Seq = next

	if _, err := tx.Exec(`
		INSERT INTO events (job_id, seq, at_ms, kind, from_state, to_state, actor, cause)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.JobID.String(), e.Seq, toMs(e.At), string(e.Kind),
		string(e.From), string(e.To), e.Actor, e.Cause); err != nil {
		return fmt.Errorf("store: append event: %w", err)
	}
	return nil
}

// JobEvents returns a job's history in order.
func (s *Store) JobEvents(jobID model.ID) ([]*model.Event, error) {
	rows, err := s.db.Query(`
		SELECT seq, at_ms, kind, from_state, to_state, actor, cause
		FROM events WHERE job_id = ? ORDER BY seq`, jobID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Event
	for rows.Next() {
		var (
			seq                          int64
			atMs                         int64
			kind, from, to, actor, cause string
		)
		if err := rows.Scan(&seq, &atMs, &kind, &from, &to, &actor, &cause); err != nil {
			return nil, err
		}
		out = append(out, &model.Event{
			JobID: jobID,
			Seq:   seq,
			At:    fromMs(atMs),
			Kind:  model.EventKind(kind),
			From:  model.JobState(from),
			To:    model.JobState(to),
			Actor: actor,
			Cause: cause,
		})
	}
	return out, rows.Err()
}
