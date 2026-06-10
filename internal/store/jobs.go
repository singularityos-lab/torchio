package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/singularityos-lab/torchio/internal/model"
)

// SaveJob inserts or updates a job row. The event log is written separately via
// AppendEvent so state transitions and their history stay distinct concerns.
func (s *Store) SaveJob(j *model.Job) error {
	ticket, err := json.Marshal(j.Ticket)
	if err != nil {
		return fmt.Errorf("store: marshal ticket: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO jobs (id, printer_id, name, state, ticket_json, doc_sha256,
		                  doc_format, doc_size, remote_job_id, error, created_ms, updated_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, state=excluded.state, ticket_json=excluded.ticket_json,
			doc_sha256=excluded.doc_sha256, doc_format=excluded.doc_format,
			doc_size=excluded.doc_size, remote_job_id=excluded.remote_job_id,
			error=excluded.error, updated_ms=excluded.updated_ms`,
		j.ID.String(), j.PrinterID.String(), j.Name, j.State, string(ticket),
		j.Document.SHA256, string(j.Document.Format), j.Document.Size,
		j.RemoteJobID, j.Error, toMs(j.Created), toMs(j.Updated))
	if err != nil {
		return fmt.Errorf("store: save job: %w", err)
	}
	return nil
}

// GetJob returns a job by ID.
func (s *Store) GetJob(id model.ID) (*model.Job, error) {
	return s.scanJob(s.db.QueryRow(jobCols+` WHERE id = ?`, id.String()))
}

// JobFilter narrows ListJobs. Zero-valued fields are ignored.
type JobFilter struct {
	PrinterID model.ID
	State     model.JobState
	// Active, when true, returns only non-terminal jobs.
	Active bool
}

// ListJobs returns jobs matching the filter, newest last (creation order).
func (s *Store) ListJobs(f JobFilter) ([]*model.Job, error) {
	q := jobCols + ` WHERE 1=1`
	var args []any
	if !f.PrinterID.IsZero() {
		q += ` AND printer_id = ?`
		args = append(args, f.PrinterID.String())
	}
	if f.State != "" {
		q += ` AND state = ?`
		args = append(args, string(f.State))
	}
	if f.Active {
		q += ` AND state NOT IN ('completed','canceled','aborted')`
	}
	q += ` ORDER BY id`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Job
	for rows.Next() {
		j, err := s.scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ReferencedDigests returns the set of spool digests still referenced by some
// job. The spool garbage collector deletes any blob not in this set.
func (s *Store) ReferencedDigests() (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT DISTINCT doc_sha256 FROM jobs WHERE doc_sha256 != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		set[d] = struct{}{}
	}
	return set, rows.Err()
}

const jobCols = `SELECT id, printer_id, name, state, ticket_json, doc_sha256, doc_format, doc_size, remote_job_id, error, created_ms, updated_ms FROM jobs`

func (s *Store) scanJob(row rowScanner) (*model.Job, error) {
	var (
		idStr, printerIDStr, name, state, ticketJSON string
		docSHA, docFormat, errStr                    string
		docSize                                      int64
		remoteJobID                                  int
		createdMs, updatedMs                         int64
	)
	if err := row.Scan(&idStr, &printerIDStr, &name, &state, &ticketJSON,
		&docSHA, &docFormat, &docSize, &remoteJobID, &errStr, &createdMs, &updatedMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	id, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("store: corrupt job id %q: %w", idStr, err)
	}
	pid, err := model.ParseID(printerIDStr)
	if err != nil {
		return nil, fmt.Errorf("store: corrupt printer id %q: %w", printerIDStr, err)
	}
	j := &model.Job{
		ID:          id,
		PrinterID:   pid,
		Name:        name,
		State:       state,
		RemoteJobID: remoteJobID,
		Error:       errStr,
		Document: model.DocRef{
			SHA256: docSHA,
			Format: model.MIME(docFormat),
			Size:   docSize,
		},
		Created: fromMs(createdMs),
		Updated: fromMs(updatedMs),
	}
	if err := json.Unmarshal([]byte(ticketJSON), &j.Ticket); err != nil {
		return nil, fmt.Errorf("store: corrupt ticket for job %q: %w", idStr, err)
	}
	return j, nil
}
