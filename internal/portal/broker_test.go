package portal

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

func TestBrokerPortal(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "broker.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := make(chan brokerRequest, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req brokerRequest
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		_ = json.Unmarshal(line, &req)
		got <- req
		conn.Write([]byte(`{"decision":"allow_session"}` + "\n"))
	}()
	t.Setenv("USH_BROKER_SOCK", socket)
	p, err := NewBroker("s1")
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.RequestPermission(context.Background(), CategoryNetwork, "ipp://printer.local:631/ipp/print", "add a printer")
	if err != nil || d != AllowSession {
		t.Fatalf("decision %q, error %v", d, err)
	}
	req := <-got
	if req.Method != "RequestPermission" || req.Category != CategoryNetwork || req.Resource != "ipp://printer.local:631/ipp/print" || req.SessionID != "s1" {
		t.Errorf("unexpected request %+v", req)
	}
}

func TestBrokerMissing(t *testing.T) {
	t.Setenv("USH_BROKER_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	if _, err := NewBroker(""); err == nil {
		t.Fatal("a missing broker socket should be an error")
	}
}
