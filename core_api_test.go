package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type coreTestService struct{}

func (coreTestService) Add(a, b int) int                { return a + b }
func (coreTestService) Fail() error                     { return errors.New("boom") }
func (coreTestService) Pair(s []string) (string, error) { return strings.Join(s, "+"), nil }
func (coreTestService) ServiceStartup() error           { return nil }
func (coreTestService) Notify(name string)              { emitEvent(name, map[string]int{"n": 1}) }

func TestCoreAPIRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "core") // short: unix socket paths are limited to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "core.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go serveCore(ctx, socket, map[string]any{"Test": coreTestService{}})

	var conn net.Conn
	for i := 0; i < 100; i++ {
		if conn, err = net.Dial("unix", socket); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if st, _ := os.Stat(socket); st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", st.Mode())
	}

	lines := bufio.NewScanner(conn)
	send := func(id int, method string, args ...any) map[string]any {
		raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "args": args})
		if _, err := conn.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
		for lines.Scan() {
			var msg map[string]any
			if err := json.Unmarshal(lines.Bytes(), &msg); err != nil {
				t.Fatal(err)
			}
			if msg["id"] == float64(id) {
				return msg
			}
		}
		t.Fatalf("no response to %s", method)
		return nil
	}

	if r := send(1, "Test.Add", 2, 3); r["result"] != float64(5) {
		t.Fatalf("Add: %v", r)
	}
	if r := send(2, "Test.Pair", []string{"a", "b"}); r["result"] != "a+b" {
		t.Fatalf("Pair: %v", r)
	}
	if r := send(3, "Test.Fail"); r["error"] != "boom" {
		t.Fatalf("Fail: %v", r)
	}
	for _, m := range []string{"Test.ServiceStartup", "Test.Missing", "Other.Add"} {
		if r := send(4, m); !strings.Contains(r["error"].(string), "unknown method") {
			t.Fatalf("%s: %v", m, r)
		}
	}
	if r := send(5, "Test.Add", 1); !strings.Contains(r["error"].(string), "takes 2 arguments") {
		t.Fatalf("arity: %v", r)
	}

	// Events reach subscribed clients alongside responses.
	raw, _ := json.Marshal(map[string]any{"id": 6, "method": "Test.Notify", "args": []any{"dockiva:test"}})
	conn.Write(append(raw, '\n'))
	sawEvent := false
	for !sawEvent && lines.Scan() {
		sawEvent = strings.Contains(lines.Text(), `"event":"dockiva:test"`)
	}
	if !sawEvent {
		t.Fatal("event not delivered")
	}
}
