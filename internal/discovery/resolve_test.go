package discovery

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func answerOnce(t *testing.T, conn *net.UDPConn, owner string, ip [4]byte) {
	t.Helper()
	buf := make([]byte, 1500)
	n, from, err := conn.ReadFromUDP(buf)
	if err != nil {
		return
	}
	var q dnsmessage.Message
	if q.Unpack(buf[:n]) != nil {
		return
	}
	reply, err := (&dnsmessage.Message{
		Header: dnsmessage.Header{Response: true, Authoritative: true},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(owner), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120},
			Body:   &dnsmessage.AResource{A: ip},
		}},
	}).Pack()
	if err == nil {
		_, _ = conn.WriteToUDP(reply, from)
	}
}

func TestResolverIgnoresCaseOfTheAnswer(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go answerOnce(t, conn, "itbsprn01.local.", [4]byte{10, 20, 10, 7})

	r := Resolver{Server: conn.LocalAddr().(*net.UDPAddr), Wait: time.Second}
	ips, err := r.Lookup(context.Background(), "ITBSPRN01.local")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.IPv4(10, 20, 10, 7)) {
		t.Fatalf("ips = %v", ips)
	}
}

func TestResolverSkipsOtherHosts(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go answerOnce(t, conn, "other.local.", [4]byte{10, 0, 0, 1})

	r := Resolver{Server: conn.LocalAddr().(*net.UDPAddr), Wait: 200 * time.Millisecond}
	if _, err := r.Lookup(context.Background(), "printer.local"); err == nil {
		t.Fatal("expected no answer")
	}
}

func TestIsLocal(t *testing.T) {
	for host, want := range map[string]bool{
		"ITBSPRN01.local": true,
		"printer.local.":  true,
		"10.0.0.1":        false,
		"printer.lan":     false,
		"local":           false,
	} {
		if IsLocal(host) != want {
			t.Errorf("IsLocal(%q) != %v", host, want)
		}
	}
}
