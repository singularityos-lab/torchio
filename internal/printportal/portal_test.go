package printportal

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/singularityos-lab/torchio/internal/model"
)

// fakeJobs records what the portal submitted.
type fakeJobs struct {
	def    string
	defErr error

	gotPrinter string
	gotName    string
	gotTicket  model.JobTicket
	gotDoc     []byte
	submitErr  error
}

func (f *fakeJobs) DefaultPrinterName() (string, error) { return f.def, f.defErr }

func (f *fakeJobs) SubmitByName(_ context.Context, printer, name string, ticket model.JobTicket, doc io.Reader) (string, error) {
	f.gotPrinter = printer
	f.gotName = name
	f.gotTicket = ticket
	f.gotDoc, _ = io.ReadAll(doc)
	if f.submitErr != nil {
		return "", f.submitErr
	}
	return "01JOBID", nil
}

func TestSettingsToTicket(t *testing.T) {
	tk := settingsToTicket(map[string]string{
		"n-copies":          "3",
		"duplex":            "horizontal",
		"media":             "iso_a4_210x297mm",
		"output-color-mode": "monochrome",
	}, nil)
	if tk.Copies != 3 {
		t.Errorf("copies = %d", tk.Copies)
	}
	if tk.Sides != "two-sided-long-edge" {
		t.Errorf("sides = %q", tk.Sides)
	}
	if tk.Media != "iso_a4_210x297mm" {
		t.Errorf("media = %q", tk.Media)
	}
	if tk.Color != "monochrome" {
		t.Errorf("color = %q", tk.Color)
	}
}

func TestPrepareThenPrint(t *testing.T) {
	jobs := &fakeJobs{def: "office"}
	b := New(jobs)

	resp, echo, tok := b.preparePrint("My Report",
		map[string]string{"printer": "office", "n-copies": "2", "duplex": "vertical"}, nil)
	if resp != responseSuccess {
		t.Fatalf("prepare response = %d", resp)
	}
	if tok == 0 {
		t.Fatal("expected a non-zero token")
	}
	if echo["n-copies"] != "2" {
		t.Errorf("echo should carry settings back: %+v", echo)
	}

	doc := "%PDF-1.7\nhello from a GTK app\n%%EOF\n"
	r, err := b.print(context.Background(), "My Report", strings.NewReader(doc), tok, nil)
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	if r != responseSuccess {
		t.Errorf("print response = %d", r)
	}
	if jobs.gotPrinter != "office" {
		t.Errorf("printer = %q, want office", jobs.gotPrinter)
	}
	if jobs.gotTicket.Copies != 2 || jobs.gotTicket.Sides != "two-sided-short-edge" {
		t.Errorf("ticket = %+v", jobs.gotTicket)
	}
	if string(jobs.gotDoc) != doc {
		t.Errorf("doc mismatch: got %q", jobs.gotDoc)
	}

	// The token must be single-use: a second print with it falls back to the
	// default printer (still "office" here) rather than the prepared state.
	if r2, err := b.print(context.Background(), "again", strings.NewReader(doc), tok, nil); err != nil || r2 != responseSuccess {
		t.Errorf("second print = %d, %v", r2, err)
	}
}

func TestPrintWithoutTokenUsesDefault(t *testing.T) {
	jobs := &fakeJobs{def: "home"}
	b := New(jobs)
	doc := "%PDF-1.7\ndirect print\n"
	r, err := b.print(context.Background(), "doc", strings.NewReader(doc),
		0, map[string]string{"n-copies": "5"})
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	if r != responseSuccess {
		t.Errorf("response = %d", r)
	}
	if jobs.gotPrinter != "home" {
		t.Errorf("printer = %q, want home (default)", jobs.gotPrinter)
	}
	if jobs.gotTicket.Copies != 5 {
		t.Errorf("copies = %d, want 5 (from inline options)", jobs.gotTicket.Copies)
	}
}

func TestPrintNoPrinterFails(t *testing.T) {
	jobs := &fakeJobs{defErr: errors.New("no default")}
	b := New(jobs)
	r, err := b.print(context.Background(), "doc", strings.NewReader("%PDF-1.7\n"), 0, nil)
	if err == nil {
		t.Fatal("expected an error when there is no printer to print to")
	}
	if r != responseEnded {
		t.Errorf("response = %d, want ended", r)
	}
}
