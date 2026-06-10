package spool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPutOpenRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.7\nhello torchio\n")
	digest, size, err := s.Put(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	if digest != want {
		t.Errorf("digest = %s, want %s", digest, want)
	}
	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}
	if !s.Exists(digest) {
		t.Error("blob should exist after Put")
	}

	rc, err := s.Open(digest)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Errorf("read back %q, want %q", got, data)
	}
}

func TestPutDeduplicates(t *testing.T) {
	s, _ := Open(t.TempDir())
	data := []byte(strings.Repeat("x", 4096))
	d1, _, err := s.Put(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	d2, _, err := s.Put(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("identical content produced different digests: %s vs %s", d1, d2)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	s, _ := Open(t.TempDir())
	digest, _, _ := s.Put(strings.NewReader("integrity matters"))

	ok, err := s.Verify(digest)
	if err != nil || !ok {
		t.Fatalf("Verify of good blob = %v, %v; want true, nil", ok, err)
	}

	// Corrupt the stored blob and confirm Verify catches it.
	path := s.pathFor(digest)
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err = s.Verify(digest)
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if ok {
		t.Error("Verify should have detected corruption")
	}
}

func TestOpenMissing(t *testing.T) {
	s, _ := Open(t.TempDir())
	_, err := s.Open("deadbeef")
	if err != ErrNotFound {
		t.Errorf("Open missing = %v, want ErrNotFound", err)
	}
}

func TestRemove(t *testing.T) {
	s, _ := Open(t.TempDir())
	digest, _, _ := s.Put(strings.NewReader("temporary"))
	if err := s.Remove(digest); err != nil {
		t.Fatal(err)
	}
	if s.Exists(digest) {
		t.Error("blob should be gone after Remove")
	}
	// Removing again is not an error.
	if err := s.Remove(digest); err != nil {
		t.Errorf("double Remove: %v", err)
	}
}
