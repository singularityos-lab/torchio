// Package spool is Torchio's content-addressed document store. Documents are
// keyed by the SHA-256 of their bytes, so re-printing the same document costs
// zero extra storage and integrity is verifiable by construction. The spool
// holds only blobs: there are no per-job control files (CUPS keeps c00001-style
// control files next to the data; Torchio keeps all job state in SQLite). Refs
// and garbage collection are the store layer's concern, not the spool's.
package spool

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrNotFound is returned when a digest is not present in the spool.
var ErrNotFound = errors.New("spool: blob not found")

// Spool is a directory of content-addressed blobs.
type Spool struct {
	root string
}

// Open prepares a spool rooted at dir, creating it if necessary.
func Open(dir string) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("spool: create root: %w", err)
	}
	return &Spool{root: dir}, nil
}

// pathFor returns the on-disk path for a digest, sharded by the first byte so
// no single directory accumulates every blob.
func (s *Spool) pathFor(digest string) string {
	if len(digest) < 2 {
		return filepath.Join(s.root, "sha256", digest)
	}
	return filepath.Join(s.root, "sha256", digest[:2], digest)
}

// Put streams r into the spool, returning the SHA-256 digest (hex) and the
// number of bytes stored. It is atomic: the blob is written to a temp file,
// fsynced, then renamed into place, so a crash never leaves a partial blob at a
// content address. Storing a blob that already exists is a cheap no-op.
func (s *Spool) Put(r io.Reader) (digest string, size int64, err error) {
	tmpDir := filepath.Join(s.root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", 0, fmt.Errorf("spool: create tmp: %w", err)
	}
	tmp, err := os.CreateTemp(tmpDir, "ingest-*")
	if err != nil {
		return "", 0, fmt.Errorf("spool: temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we don't rename it away.
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		tmp.Close()
		return "", 0, fmt.Errorf("spool: write: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return "", 0, fmt.Errorf("spool: fsync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("spool: close: %w", err)
	}

	digest = hex.EncodeToString(h.Sum(nil))
	dst := s.pathFor(digest)
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, fmt.Errorf("spool: create shard: %w", err)
	}

	// If the blob already exists, drop the temp file and keep the existing one.
	if _, statErr := os.Stat(dst); statErr == nil {
		_ = os.Remove(tmpName)
		return digest, size, nil
	}

	if err = os.Rename(tmpName, dst); err != nil {
		return "", 0, fmt.Errorf("spool: commit: %w", err)
	}
	if err = syncDir(filepath.Dir(dst)); err != nil {
		return "", 0, fmt.Errorf("spool: fsync dir: %w", err)
	}
	return digest, size, nil
}

// Open returns a reader for the blob with the given digest.
func (s *Spool) Open(digest string) (io.ReadCloser, error) {
	f, err := os.Open(s.pathFor(digest))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Exists reports whether a blob is present.
func (s *Spool) Exists(digest string) bool {
	_, err := os.Stat(s.pathFor(digest))
	return err == nil
}

// Remove deletes a blob. The store layer calls this only when the refcount
// reaches zero. Removing an absent blob is not an error.
func (s *Spool) Remove(digest string) error {
	err := os.Remove(s.pathFor(digest))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Verify recomputes the digest of a stored blob and checks it matches its
// address, the basis for `torchio doctor --fsck`.
func (s *Spool) Verify(digest string) (bool, error) {
	rc, err := s.Open(digest)
	if err != nil {
		return false, err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == digest, nil
}

// syncDir fsyncs a directory so a rename is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
