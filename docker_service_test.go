package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// Output reaches clients byte for byte: a character split across reads and
// bytes that are not UTF-8 survive the JSON event.
func TestTerminalOutputEventsAreByteExact(t *testing.T) {
	events := make(chan coreEvent, 16)
	coreSubscribers.Lock()
	coreSubscribers.set[events] = struct{}{}
	coreSubscribers.Unlock()
	defer func() {
		coreSubscribers.Lock()
		delete(coreSubscribers.set, events)
		coreSubscribers.Unlock()
	}()

	pr, pw := io.Pipe()
	conn, _ := net.Pipe()
	session := &terminalSession{id: "s1", containerID: "c1",
		response: client.ExecAttachResult{HijackedResponse: client.HijackedResponse{Conn: conn, Reader: bufio.NewReaderSize(pr, 16)}}}
	s := &DockerService{terminalSessions: map[string]*terminalSession{"s1": session}}
	go s.readTerminal(session)

	// "✓" split across two reads, then a lone 0xff and an 8-bit CSI (0x9b).
	want := []byte("\xe2\x9c\x93 ok\n" + strings.Repeat("é", 40) + "\xff\x9b")
	go func() {
		pw.Write(want[:2])
		time.Sleep(20 * time.Millisecond)
		pw.Write(want[2:])
		pw.Close()
	}()

	var got []byte
	for {
		var ev coreEvent
		select {
		case ev = <-events:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out; got %q", got)
		}
		wire, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var msg struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(wire, &msg); err != nil {
			t.Fatal(err)
		}
		if _, ok := msg.Data["data"]; ok {
			t.Fatalf("event still carries a lossy string: %s", wire)
		}
		if msg.Data["closed"] == true {
			break
		}
		chunk, err := base64.StdEncoding.DecodeString(msg.Data["dataBase64"].(string))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, chunk...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// SendTerminalBytes takes base64 over the core API and writes the exact bytes.
func TestSendTerminalBytesDecodesBase64(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	s := &DockerService{terminalSessions: map[string]*terminalSession{
		"s1": {id: "s1", response: client.ExecAttachResult{HijackedResponse: client.HijackedResponse{Conn: conn}}},
	}}
	services := map[string]any{"DockerService": s}

	// An X10 mouse report past column 95 and an 8-bit CSI: not valid UTF-8.
	want := []byte("\x1b[M \xff\xff\x9b")
	arg, _ := json.Marshal(base64.StdEncoding.EncodeToString(want))
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := peer.Read(buf)
		got <- buf[:n]
	}()
	if _, err := coreCall(services, "DockerService.SendTerminalBytes", []json.RawMessage{json.RawMessage(`"s1"`), arg}); err != nil {
		t.Fatal(err)
	}
	if b := <-got; !bytes.Equal(b, want) {
		t.Fatalf("got %q, want %q", b, want)
	}

	if _, err := coreCall(services, "DockerService.SendTerminalBytes", []json.RawMessage{json.RawMessage(`"s1"`), json.RawMessage(`"not base64!"`)}); err == nil {
		t.Fatal("invalid base64 was accepted")
	}

	// The text method still works for the Vue app.
	go func() {
		buf := make([]byte, 64)
		n, _ := peer.Read(buf)
		got <- buf[:n]
	}()
	if err := s.SendTerminalInput("s1", "ls ✓\n"); err != nil {
		t.Fatal(err)
	}
	if b := <-got; string(b) != "ls ✓\n" {
		t.Fatalf("got %q", b)
	}
}
