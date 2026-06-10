// Package ipp is Torchio's IPP client. Principle P1 says every print
// destination is an IPP endpoint, so this package is the one place that
// actually speaks the wire protocol: it builds goipp messages, posts them over
// HTTP(S), decodes the reply and turns the IPP status into a Go error. Higher
// layers (the CLI today, the spooler tomorrow) only ever see typed requests
// and results.
package ipp

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/OpenPrinting/goipp"
)

// Client is a reusable IPP client. The zero value is not usable; construct it
// with NewClient. It is safe for concurrent use.
type Client struct {
	http      *http.Client
	userName  string
	version   goipp.Version
	requestID atomic.Uint32
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying HTTP client (timeouts, proxies,
// custom TLS). By default the client allows self-signed printer certificates,
// because IPPS trust in Torchio is established out of band via TOFU and the
// ush portal, not via the public CA system.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithUserName sets the value sent as "requesting-user-name".
func WithUserName(name string) Option { return func(c *Client) { c.userName = name } }

// NewClient builds an IPP client with sensible defaults.
func NewClient(opts ...Option) *Client {
	c := &Client{
		userName: "torchio",
		version:  goipp.DefaultVersion, // IPP 2.0
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// TOFU is enforced a layer up; do not reject self-signed
				// printer certs at the transport level.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// nextID returns a monotonically increasing request id (never zero).
func (c *Client) nextID() uint32 {
	id := c.requestID.Add(1)
	if id == 0 {
		id = c.requestID.Add(1)
	}
	return id
}

// baseOperation appends the three attributes every IPP request must carry:
// the charset, the natural language and the target printer-uri.
func baseOperation(msg *goipp.Message, printerURI string) {
	msg.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	msg.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
	msg.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(printerURI)))
}

// do encodes msg, posts it (optionally followed by a document body) to the
// printer, decodes the reply and verifies the IPP status is a success. The
// returned message is the decoded response on success.
func (c *Client) do(ctx context.Context, transport string, msg *goipp.Message, doc io.Reader) (*goipp.Message, error) {
	payload, err := msg.EncodeBytes()
	if err != nil {
		return nil, fmt.Errorf("encode IPP request: %w", err)
	}

	var body io.Reader = bytes.NewReader(payload)
	if doc != nil {
		// Print-Job sends the document bytes immediately after the IPP
		// message in the same request body (not multipart).
		body = io.MultiReader(bytes.NewReader(payload), doc)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, transport, body)
	if err != nil {
		return nil, fmt.Errorf("build HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", goipp.ContentType)
	httpReq.Header.Set("Accept", goipp.ContentType)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("IPP transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("IPP HTTP status %s", resp.Status)
	}

	var out goipp.Message
	if err := out.Decode(resp.Body); err != nil {
		return nil, fmt.Errorf("decode IPP response: %w", err)
	}

	if status := goipp.Status(out.Code); !statusOK(status) {
		return &out, &StatusError{Status: status, Detail: statusMessage(&out)}
	}
	return &out, nil
}

// GetPrinterAttributes performs Get-Printer-Attributes. The printer's
// attributes are returned in the message's Printer group. Pass requested to
// limit the attributes asked for; empty means "all".
func (c *Client) GetPrinterAttributes(ctx context.Context, printerURI string, requested ...string) (*goipp.Message, error) {
	canon, transport, err := normalizeURI(printerURI)
	if err != nil {
		return nil, err
	}
	msg := goipp.NewRequest(c.version, goipp.OpGetPrinterAttributes, c.nextID())
	baseOperation(msg, canon)
	if len(requested) == 0 {
		requested = []string{"all"}
	}
	vals := make([]goipp.Value, len(requested))
	for i, r := range requested {
		vals[i] = goipp.String(r)
	}
	msg.Operation.Add(goipp.MakeAttr("requested-attributes", goipp.TagKeyword, vals[0], vals[1:]...))
	return c.do(ctx, transport, msg, nil)
}

// PrintRequest describes a single Print-Job (or Validate-Job) submission. Only
// PrinterURI and Document are mandatory for Print-Job; the rest are job
// template attributes that are omitted when zero.
type PrintRequest struct {
	PrinterURI     string
	JobName        string
	DocumentFormat string // MIME type, e.g. application/pdf
	Copies         int
	Sides          string // one-sided, two-sided-long-edge, two-sided-short-edge
	Media          string // e.g. iso_a4_210x297mm
	Document       io.Reader
}

// jobMessage builds a Print-Job or Validate-Job request from r.
func (c *Client) jobMessage(op goipp.Op, canon string, r PrintRequest) *goipp.Message {
	msg := goipp.NewRequest(c.version, op, c.nextID())
	baseOperation(msg, canon)
	msg.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName, goipp.String(c.userName)))
	if r.JobName != "" {
		msg.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String(r.JobName)))
	}
	if r.DocumentFormat != "" {
		msg.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(r.DocumentFormat)))
	}
	if r.Copies > 0 {
		msg.Job.Add(goipp.MakeAttribute("copies", goipp.TagInteger, goipp.Integer(r.Copies)))
	}
	if r.Sides != "" {
		msg.Job.Add(goipp.MakeAttribute("sides", goipp.TagKeyword, goipp.String(r.Sides)))
	}
	if r.Media != "" {
		msg.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String(r.Media)))
	}
	return msg
}

// ValidateJob performs Validate-Job: it asks the printer to check the job
// template without submitting a document. A nil error means the job would be
// accepted.
func (c *Client) ValidateJob(ctx context.Context, r PrintRequest) error {
	canon, transport, err := normalizeURI(r.PrinterURI)
	if err != nil {
		return err
	}
	msg := c.jobMessage(goipp.OpValidateJob, canon, r)
	_, err = c.do(ctx, transport, msg, nil)
	return err
}

// PrintResult is the outcome of a successful Print-Job.
type PrintResult struct {
	JobID    int
	JobState int    // IPP job-state enum, if reported
	JobURI   string // job-uri, if reported
}

// PrintJob submits r.Document to the printer. The document is streamed, so the
// caller's Reader is consumed exactly once.
func (c *Client) PrintJob(ctx context.Context, r PrintRequest) (*PrintResult, error) {
	canon, transport, err := normalizeURI(r.PrinterURI)
	if err != nil {
		return nil, err
	}
	msg := c.jobMessage(goipp.OpPrintJob, canon, r)
	out, err := c.do(ctx, transport, msg, r.Document)
	if err != nil {
		return nil, err
	}
	res := &PrintResult{}
	if id, ok := FirstInt(out.Job, "job-id"); ok {
		res.JobID = id
	}
	if st, ok := FirstInt(out.Job, "job-state"); ok {
		res.JobState = st
	}
	if uri, ok := FirstString(out.Job, "job-uri"); ok {
		res.JobURI = uri
	}
	return res, nil
}

// GetJobAttributes performs Get-Job-Attributes for a job previously created on
// the printer. Used by the spooler to reconcile state after a restart.
func (c *Client) GetJobAttributes(ctx context.Context, printerURI string, jobID int) (*goipp.Message, error) {
	canon, transport, err := normalizeURI(printerURI)
	if err != nil {
		return nil, err
	}
	msg := goipp.NewRequest(c.version, goipp.OpGetJobAttributes, c.nextID())
	baseOperation(msg, canon)
	msg.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(jobID)))
	return c.do(ctx, transport, msg, nil)
}

// CancelJob performs Cancel-Job.
func (c *Client) CancelJob(ctx context.Context, printerURI string, jobID int) error {
	canon, transport, err := normalizeURI(printerURI)
	if err != nil {
		return err
	}
	msg := goipp.NewRequest(c.version, goipp.OpCancelJob, c.nextID())
	baseOperation(msg, canon)
	msg.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(jobID)))
	_, err = c.do(ctx, transport, msg, nil)
	return err
}
