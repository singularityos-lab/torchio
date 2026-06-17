-- Torchio state schema. All mutable state lives here; the daemon never writes
-- its configuration. Times are stored as unix milliseconds. IDs are ULIDs, so
-- ordering by id is ordering by creation time.

CREATE TABLE IF NOT EXISTS printers (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    uri        TEXT    NOT NULL,
    origin     TEXT    NOT NULL,
    is_default INTEGER NOT NULL DEFAULT 0,
    caps_json  TEXT    NOT NULL DEFAULT '{}',
    attrs_raw  BLOB    NOT NULL DEFAULT x'',
    created_ms INTEGER NOT NULL,
    updated_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS jobs (
    id            TEXT    PRIMARY KEY,
    printer_id    TEXT    NOT NULL REFERENCES printers(id) ON DELETE CASCADE,
    name          TEXT    NOT NULL DEFAULT '',
    state         TEXT    NOT NULL,
    ticket_json   TEXT    NOT NULL DEFAULT '{}',
    doc_sha256    TEXT    NOT NULL DEFAULT '',
    doc_format    TEXT    NOT NULL DEFAULT '',
    doc_size      INTEGER NOT NULL DEFAULT 0,
    remote_job_id INTEGER NOT NULL DEFAULT 0,
    error         TEXT    NOT NULL DEFAULT '',
    created_ms    INTEGER NOT NULL,
    updated_ms    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS jobs_by_printer ON jobs(printer_id, id);
CREATE INDEX IF NOT EXISTS jobs_by_state ON jobs(state);

CREATE TABLE IF NOT EXISTS events (
    job_id     TEXT    NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    at_ms      INTEGER NOT NULL,
    kind       TEXT    NOT NULL,
    from_state TEXT    NOT NULL DEFAULT '',
    to_state   TEXT    NOT NULL DEFAULT '',
    actor      TEXT    NOT NULL DEFAULT '',
    cause      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (job_id, seq)
);

-- Discovery is read-only by constitution: this table is an ephemeral cache of
-- what mDNS has seen. Nothing here becomes a printer without explicit user
-- confirmation through the portal.
CREATE TABLE IF NOT EXISTS devices_seen (
    key          TEXT    PRIMARY KEY,
    uri          TEXT    NOT NULL,
    name         TEXT    NOT NULL,
    last_seen_ms INTEGER NOT NULL
);
