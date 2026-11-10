package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The app follows Dockiva Engine's own /events stream, not the docker CLI's
// current context, so containers started outside the app show up.
func TestVMFreeStreamEvents(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "dkv-ev") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	token := strings.Repeat("a", 64)
	if err := os.MkdirAll(filepath.Join(home, "docker-client"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"HttpHeaders":{"X-VMFree-Token":"` + token + `"}}`
	if err := os.WriteFile(filepath.Join(home, "docker-client", "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(home, "engine.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.45/events" || r.Header.Get("X-VMFree-Token") != token {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"Type":"container","Action":"start","id":"a1"}` + "\n" + `{"Type":"container","Action":"die","id":"a1"}` + "\n"))
	}))

	var lines []string
	connected := 0
	err = vmfreeAt(home).streamEvents(context.Background(), func(line []byte) {
		lines = append(lines, string(line))
	}, func() { connected++ })
	if err != nil || connected != 1 || len(lines) != 2 || !strings.Contains(lines[1], `"die"`) {
		t.Fatalf("err %v, connected %d, lines %q", err, connected, lines)
	}
}
