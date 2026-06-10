package usb

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"

	"github.com/singularityos-lab/torchio/internal/ipp"
)

// fakeUSBTransport simulates a printer's IPP-over-USB interface: every Dial
// returns one end of an in-memory pipe whose other end runs an HTTP server with
// the given handler. This is the printer, reachable only over the "USB" pipe.
type fakeUSBTransport struct {
	handler http.Handler
}

func (f *fakeUSBTransport) Dial() (Conn, error) {
	client, device := net.Pipe()
	go http.Serve(newSingleConnListener(device), f.handler) //nolint:errcheck
	return client, nil
}

// singleConnListener feeds one already-open connection to http.Serve.
type singleConnListener struct {
	ch     chan net.Conn
	closed chan struct{}
}

func newSingleConnListener(c net.Conn) *singleConnListener {
	l := &singleConnListener{ch: make(chan net.Conn, 1), closed: make(chan struct{})}
	l.ch <- c
	return l
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *singleConnListener) Close() error   { close(l.closed); return nil }
func (l *singleConnListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "usb" }

// startBridge runs a bridge over the fake transport and returns it.
func startBridge(t *testing.T, h http.Handler) *Bridge {
	t.Helper()
	b := NewBridge(&fakeUSBTransport{handler: h})
	if err := b.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go b.Serve(ctx) //nolint:errcheck
	t.Cleanup(func() { b.Close() })
	return b
}

func TestBridgeTunnelsHTTP(t *testing.T) {
	b := startBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "from the printer")
	}))

	// A plain HTTP client reaches the printer through the USB bridge.
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get("http://" + b.Addr() + "/hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from the printer" {
		t.Errorf("body = %q", body)
	}
}

// TestBridgeTunnelsIPP proves a real IPP exchange works through the USB tunnel:
// the IPP client speaks to ipp://127.0.0.1:PORT, the bridge forwards it over the
// pipe, and the fake IPP printer answers Get-Printer-Attributes.
func TestBridgeTunnelsIPP(t *testing.T) {
	b := startBridge(t, http.HandlerFunc(ippPrinterHandler))

	client := ipp.NewClient()
	msg, err := client.GetPrinterAttributes(context.Background(), b.URI("/ipp/print"))
	if err != nil {
		t.Fatalf("GetPrinterAttributes over USB bridge: %v", err)
	}
	caps := ipp.ParseCapabilities(msg)
	if caps.MakeAndModel != "Torchio USB Printer" {
		t.Errorf("make/model = %q", caps.MakeAndModel)
	}
	if !caps.AcceptsFormat("application/pdf") {
		t.Errorf("expected PDF support, got %v", caps.DocumentFormats)
	}
}

// ippPrinterHandler answers a minimal Get-Printer-Attributes.
func ippPrinterHandler(w http.ResponseWriter, r *http.Request) {
	var req goipp.Message
	if err := req.Decode(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, req.RequestID)
	resp.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	resp.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
	resp.Printer.Add(goipp.MakeAttribute("printer-make-and-model", goipp.TagText, goipp.String("Torchio USB Printer")))
	resp.Printer.Add(goipp.MakeAttribute("document-format-supported", goipp.TagMimeType, goipp.String("application/pdf")))
	payload, _ := resp.EncodeBytes()
	w.Header().Set("Content-Type", goipp.ContentType)
	_, _ = w.Write(payload)
}
