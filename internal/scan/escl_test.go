package scan

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeScanner implements the eSCL endpoints a Scanner uses.
func fakeScanner(t *testing.T, image []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/eSCL/ScannerCapabilities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, `<scan:ScannerCapabilities/>`)
	})
	mux.HandleFunc("/eSCL/ScanJobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		// The settings body must be well-formed enough to carry our choices.
		if !strings.Contains(string(body), "<scan:XResolution>600</scan:XResolution>") {
			t.Errorf("scan settings missing resolution: %s", body)
		}
		w.Header().Set("Location", "/eSCL/ScanJobs/job1")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/eSCL/ScanJobs/job1/NextDocument", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(image)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestScannerCapabilities(t *testing.T) {
	srv := fakeScanner(t, nil)
	sc := NewScanner(srv.URL + "/eSCL")
	caps, err := sc.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !strings.Contains(string(caps), "ScannerCapabilities") {
		t.Errorf("caps = %q", caps)
	}
}

func TestScannerScan(t *testing.T) {
	want := []byte("\xff\xd8\xff\xe0 fake jpeg scan data")
	srv := fakeScanner(t, want)
	sc := NewScanner(srv.URL + "/eSCL")

	rc, err := sc.Scan(context.Background(), Settings{Resolution: 600, Format: "image/jpeg"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("scanned bytes = %q, want %q", got, want)
	}
}

func TestScannerScanError(t *testing.T) {
	// A scanner that never returns a Location must surface an error.
	mux := http.NewServeMux()
	mux.HandleFunc("/eSCL/ScanJobs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	sc := NewScanner(srv.URL + "/eSCL")
	if _, err := sc.Scan(context.Background(), Settings{}); err == nil {
		t.Error("expected an error when the scanner refuses the job")
	}
}
