// Package scan is Torchio's eSCL (AirScan) client: scanners are printers in
// reverse on the same mDNS fabric, so the same driverless model applies. It
// speaks the HTTP-based eSCL protocol to pull a scan from a network scanner,
// with no vendor driver.
package scan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Scanner is an eSCL scanner reachable at a base URL (e.g. http://host:8080/eSCL).
type Scanner struct {
	base string
	http *http.Client
}

// NewScanner builds a client for the given eSCL base URL.
func NewScanner(baseURL string) *Scanner {
	return &Scanner{
		base: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// Capabilities fetches the raw ScannerCapabilities XML.
func (s *Scanner) Capabilities(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/ScannerCapabilities", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scan: capabilities: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scan: capabilities status %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Settings are the subset of eSCL scan settings Torchio sets.
type Settings struct {
	Format     string // DocumentFormatExt, e.g. image/jpeg or application/pdf
	ColorMode  string // RGB24 | Grayscale8 | BlackAndWhite1
	Resolution int    // DPI (X and Y)
	Source     string // Platen | Feeder
}

// withDefaults fills sensible defaults.
func (st Settings) withDefaults() Settings {
	if st.Format == "" {
		st.Format = "image/jpeg"
	}
	if st.ColorMode == "" {
		st.ColorMode = "RGB24"
	}
	if st.Resolution == 0 {
		st.Resolution = 300
	}
	if st.Source == "" {
		st.Source = "Platen"
	}
	return st
}

// Scan starts a scan job and returns a reader of the scanned document. The
// caller reads and closes it. The flow is the standard eSCL one: POST ScanJobs
// (the scanner replies 201 with the job's Location), then GET the job's
// NextDocument.
func (s *Scanner) Scan(ctx context.Context, st Settings) (io.ReadCloser, error) {
	st = st.withDefaults()

	body := scanSettingsXML(st)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/ScanJobs", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scan: create job: %w", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("scan: create job status %s", resp.Status)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil, fmt.Errorf("scan: scanner did not return a job Location")
	}

	jobURL, err := s.resolve(loc)
	if err != nil {
		return nil, err
	}

	docReq, err := http.NewRequestWithContext(ctx, http.MethodGet, jobURL+"/NextDocument", nil)
	if err != nil {
		return nil, err
	}
	docResp, err := s.http.Do(docReq)
	if err != nil {
		return nil, fmt.Errorf("scan: fetch document: %w", err)
	}
	if docResp.StatusCode != http.StatusOK {
		docResp.Body.Close()
		return nil, fmt.Errorf("scan: document status %s", docResp.Status)
	}
	return docResp.Body, nil
}

// resolve turns a Location header (absolute or relative) into an absolute URL.
func (s *Scanner) resolve(loc string) (string, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("scan: bad job Location %q: %w", loc, err)
	}
	if u.IsAbs() {
		return strings.TrimRight(loc, "/"), nil
	}
	baseU, err := url.Parse(s.base)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(baseU.ResolveReference(u).String(), "/"), nil
}

// scanSettingsXML renders the eSCL ScanSettings request body.
func scanSettingsXML(st Settings) string {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<scan:ScanSettings xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">` + "\n")
	b.WriteString("  <pwg:Version>2.0</pwg:Version>\n")
	b.WriteString("  <scan:Intent>Document</scan:Intent>\n")
	b.WriteString("  <scan:DocumentFormatExt>" + st.Format + "</scan:DocumentFormatExt>\n")
	b.WriteString("  <scan:ColorMode>" + st.ColorMode + "</scan:ColorMode>\n")
	b.WriteString("  <scan:XResolution>" + strconv.Itoa(st.Resolution) + "</scan:XResolution>\n")
	b.WriteString("  <scan:YResolution>" + strconv.Itoa(st.Resolution) + "</scan:YResolution>\n")
	b.WriteString("  <pwg:InputSource>" + st.Source + "</pwg:InputSource>\n")
	b.WriteString("</scan:ScanSettings>\n")
	return b.String()
}
