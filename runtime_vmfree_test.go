package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVMFreeIdentityAndCapabilities(t *testing.T) {
	r := vmfreeAt(t.TempDir())
	defer r.Close()
	info := r.Info(context.Background())
	if info.Provider != "vmfree" || info.Connected || !info.Experimental || !info.Capabilities.Terminal || !info.Capabilities.Stats || info.Capabilities.Compose || !info.Capabilities.PullImages || !info.Capabilities.PortPublishing {
		t.Fatalf("incorrect preview identity: %+v", info)
	}
	s := &DockerService{}
	s.replaceRuntime(r)
	// With no engine running the pull fails in the VM-free client, not the Docker CLI.
	if _, err := s.PullImage("alpine"); err == nil || !strings.Contains(err.Error(), "Dockiva Engine is not ready") {
		t.Fatalf("pull did not stay on VM-free: %v", err)
	}
	if err := s.RemoveImage("other-daemon-image", true); err == nil {
		t.Fatal("unsupported removal reached Docker")
	}
	if got := runtimeProviderForBackend("vmfree"); got != "vmfree" {
		t.Fatal(got)
	}
}
func TestVMFreeUnixAuthenticationAndRotation(t *testing.T) {
	home, err := os.MkdirTemp("", "dv-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	os.Mkdir(filepath.Join(home, "docker-client"), 0700)
	token := strings.Repeat("a", 64)
	writeToken := func() {
		b, _ := json.Marshal(map[string]any{"HttpHeaders": map[string]string{"X-VMFree-Token": token}})
		if err := os.WriteFile(filepath.Join(home, "docker-client", "config.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeToken()
	listener, err := net.Listen("unix", filepath.Join(home, "engine.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		data, _ := os.ReadFile(filepath.Join(home, "docker-client", "config.json"))
		var cfg struct{ HttpHeaders map[string]string }
		json.Unmarshal(data, &cfg)
		if q.Header.Get("X-VMFree-Token") != cfg.HttpHeaders["X-VMFree-Token"] {
			w.WriteHeader(401)
			return
		}
		switch q.URL.Path {
		case "/v1.45/_ping":
			w.Write([]byte("OK"))
		case "/v1.45/images/create":
			if q.Method != "POST" || q.URL.Query().Get("fromImage") != "ghcr.io/org/app:v1" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"status":"Downloaded verified image ghcr.io/org/app:v1"}`))
		case "/v1.45/containers/json":
			w.Write([]byte(`[{"Id":"123456789012abc","Names":["/preview"],"State":"exited","Image":"demo","Ports":[{"IP":"127.0.0.1","PrivatePort":80,"PublicPort":8080,"Type":"tcp"}]}]`))
		case "/v1.45/containers/create":
			var body struct {
				HostConfig struct {
					PortBindings map[string][]map[string]string
				}
			}
			json.NewDecoder(q.Body).Decode(&body)
			if b := body.HostConfig.PortBindings["80/tcp"]; len(b) != 1 || b[0]["HostPort"] != "8080" || b[0]["HostIp"] != "127.0.0.1" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"Id":"abcdef1234567890"}`))
		default:
			w.WriteHeader(501)
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	r := vmfreeAt(home)
	defer r.Close()
	if !r.Info(context.Background()).Connected {
		t.Fatal("not connected")
	}
	token = strings.Repeat("b", 64)
	writeToken()
	if !r.Info(context.Background()).Connected {
		t.Fatal("token rotation failed")
	}
	pulled, err := r.PullRuntimeImage(context.Background(), "ghcr.io/org/app:v1")
	if err != nil || pulled.Detail != "Downloaded verified image ghcr.io/org/app:v1" {
		t.Fatalf("pull: %+v %v", pulled, err)
	}
	rows, err := r.ListContainers(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Name != "preview" || len(rows[0].Ports) != 1 || rows[0].Ports[0].Display != "8080:80/tcp" {
		t.Fatalf("%+v %v", rows, err)
	}
	if _, err = r.CreateRuntimeContainer(context.Background(), RuntimeCreateContainerRequest{Name: "web", Image: "alpine:3.20", PortMappings: []RuntimePortMapping{{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}}}); err != nil {
		t.Fatalf("port publication: %v", err)
	}
	if _, err = r.CreateRuntimeContainer(context.Background(), RuntimeCreateContainerRequest{Name: "dns", Image: "alpine:3.20", PortMappings: []RuntimePortMapping{{HostPort: 5353, ContainerPort: 53, Protocol: "udp"}}}); err == nil {
		t.Fatal("UDP publication accepted")
	}
}

// Opt-in integration against an isolated engine started by verify_dockiva.py.
func TestVMFreeLiveEngine(t *testing.T) {
	home := os.Getenv("DOCKIVA_TEST_VMFREE_HOME")
	if home == "" {
		t.Skip("isolated VM-free engine not supplied")
	}
	r := vmfreeAt(home)
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if !r.Info(ctx).Connected {
		t.Fatal("engine offline")
	}
	images, err := r.ListRuntimeImages(ctx)
	if err != nil || len(images) == 0 {
		t.Fatalf("images: %v %v", images, err)
	}
	q := RuntimeCreateContainerRequest{Name: "dockiva-preview-test", Image: "vmfree/test:demo", Command: []string{"-c", "busybox echo hello"}}
	item, err := r.CreateRuntimeContainer(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer r.RemoveContainer(context.Background(), item.ID, true)
	if err = r.StartContainer(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err = r.Inspect(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	logs, err := r.RuntimeReadLogs(ctx, item.ID, 100, 0)
	if err != nil || !logs.Closed || !strings.Contains(logs.Data, "hello") {
		t.Fatalf("logs: %+v %v", logs, err)
	}
	if err = r.StopContainer(ctx, item.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = r.RestartContainer(ctx, item.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ListContainers(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.RemoveContainer(ctx, item.ID, true); err != nil {
		t.Fatal(err)
	}
}

// TestVMFreeTerminalAndStatsUseEngineDockerSocket: with Dockiva Engine active, the
// terminal and the resource figures use the engine's own Docker socket. They were
// "the active runtime does not support terminal exec" and zeros.
func TestVMFreeTerminalAndStatsUseEngineDockerSocket(t *testing.T) {
	home, err := os.MkdirTemp("", "dv-") // short: a socket path must fit in 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	listener, err := net.Listen("unix", filepath.Join(home, "docker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	resized := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/v1.45/containers/c1/exec":
			var body struct {
				Tty bool
				Cmd []string
			}
			_ = json.NewDecoder(q.Body).Decode(&body)
			if !body.Tty || strings.Join(body.Cmd, " ") != "/bin/sh" {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"Id":"e1"}`))
		case "/v1.45/exec/e1/start": // the engine's raw TTY stream; this one echoes
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = rw.Flush()
			_, _ = io.Copy(conn, rw)
		case "/v1.45/exec/e1/resize":
			resized <- q.URL.Query().Get("h") + "x" + q.URL.Query().Get("w")
		case "/v1.45/containers/c1/stats":
			_, _ = w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":3000000000},"system_cpu_usage":20000000000,"online_cpus":4},` +
				`"precpu_stats":{"cpu_usage":{"total_usage":1000000000},"system_cpu_usage":10000000000},` +
				`"memory_stats":{"usage":52428800,"limit":0,"stats":{}},"pids_stats":{"current":7}}`))
		default:
			w.WriteHeader(404)
		}
	})}
	go server.Serve(listener)
	defer server.Close()

	events := make(chan coreEvent, 16)
	coreSubscribers.Lock()
	coreSubscribers.set[events] = struct{}{}
	coreSubscribers.Unlock()
	defer func() {
		coreSubscribers.Lock()
		delete(coreSubscribers.set, events)
		coreSubscribers.Unlock()
	}()

	r := vmfreeAt(home)
	defer r.Close()
	s := &DockerService{terminalSessions: map[string]*terminalSession{}}
	s.replaceRuntime(r)
	id, err := s.StartTerminal("c1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendTerminalInput(id, "echo hi\n"); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for !strings.Contains(string(got), "echo hi\n") {
		select {
		case ev := <-events:
			if out, ok := ev.Data.(StreamOutputEvent); ok && out.StreamID == id {
				got = append(got, out.Data...)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no output from the engine's exec; got %q", got)
		}
	}
	if err := s.ResizeTerminal(id, 100, 30); err != nil {
		t.Fatal(err)
	}
	select {
	case size := <-resized:
		if size != "30x100" {
			t.Fatalf("resized to %s", size)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the resize did not reach the engine")
	}
	s.CloseTerminal(id)

	stats := s.GetContainerStats([]string{"c1"})
	if len(stats) != 1 || stats[0].Error != "" || stats[0].MemoryUsage != 50<<20 || stats[0].PIDs != 7 || int(stats[0].CPUPercent+0.5) != 80 {
		t.Fatalf("stats: %+v", stats)
	}
}
