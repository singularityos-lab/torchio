package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/singularityos-lab/torchio/internal/model"
)

// SavePrinter inserts or updates a printer. Capabilities are stored as JSON.
func (s *Store) SavePrinter(p *model.Printer) error {
	caps, err := json.Marshal(p.Caps)
	if err != nil {
		return fmt.Errorf("store: marshal caps: %w", err)
	}
	def := 0
	if p.Default {
		def = 1
	}
	raw := p.RawAttrs
	if raw == nil {
		raw = []byte{} // a nil BLOB would violate the NOT NULL column
	}
	_, err = s.db.Exec(`
		INSERT INTO printers (id, name, uri, origin, is_default, caps_json, attrs_raw, created_ms, updated_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, uri=excluded.uri, origin=excluded.origin,
			is_default=excluded.is_default, caps_json=excluded.caps_json,
			attrs_raw=excluded.attrs_raw, updated_ms=excluded.updated_ms`,
		p.ID.String(), p.Name, p.URI, string(p.Origin), def, string(caps), raw,
		toMs(p.Created), toMs(p.Updated))
	if err != nil {
		return fmt.Errorf("store: save printer: %w", err)
	}
	return nil
}

// GetPrinter returns a printer by ID.
func (s *Store) GetPrinter(id model.ID) (*model.Printer, error) {
	return s.scanPrinter(s.db.QueryRow(printerCols+` WHERE id = ?`, id.String()))
}

// GetPrinterByName returns a printer by its user-chosen name.
func (s *Store) GetPrinterByName(name string) (*model.Printer, error) {
	return s.scanPrinter(s.db.QueryRow(printerCols+` WHERE name = ?`, name))
}

// DefaultPrinter returns the default printer, or ErrNotFound if none is set.
func (s *Store) DefaultPrinter() (*model.Printer, error) {
	return s.scanPrinter(s.db.QueryRow(printerCols + ` WHERE is_default = 1 LIMIT 1`))
}

// ListPrinters returns all printers in creation order.
func (s *Store) ListPrinters() ([]*model.Printer, error) {
	rows, err := s.db.Query(printerCols + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Printer
	for rows.Next() {
		p, err := s.scanPrinter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePrinter removes a printer (and its jobs, via cascade).
func (s *Store) DeletePrinter(id model.ID) error {
	res, err := s.db.Exec(`DELETE FROM printers WHERE id = ?`, id.String())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDefaultPrinter makes id the only default, atomically.
func (s *Store) SetDefaultPrinter(id model.ID) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`UPDATE printers SET is_default = 0`); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE printers SET is_default = 1 WHERE id = ?`, id.String())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// printerCols is the shared SELECT prefix for printers.
const printerCols = `SELECT id, name, uri, origin, is_default, caps_json, attrs_raw, created_ms, updated_ms FROM printers`

// rowScanner abstracts *sql.Row and *sql.Rows so scan helpers serve both.
type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanPrinter(row rowScanner) (*model.Printer, error) {
	var (
		idStr, name, uri, origin, capsJSON string
		rawAttrs                           []byte
		def                                int
		createdMs, updatedMs               int64
	)
	if err := row.Scan(&idStr, &name, &uri, &origin, &def, &capsJSON, &rawAttrs, &createdMs, &updatedMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	id, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("store: corrupt printer id %q: %w", idStr, err)
	}
	p := &model.Printer{
		ID:       id,
		Name:     name,
		URI:      uri,
		Origin:   model.Origin(origin),
		Default:  def != 0,
		RawAttrs: rawAttrs,
		Created:  fromMs(createdMs),
		Updated:  fromMs(updatedMs),
	}
	if err := json.Unmarshal([]byte(capsJSON), &p.Caps); err != nil {
		return nil, fmt.Errorf("store: corrupt caps for %q: %w", name, err)
	}
	return p, nil
}
