package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVMFreeBundledAutoStart(t *testing.T) {
	helper := os.Getenv("DOCKIVA_TEST_BUNDLED_HELPER")
	if helper == "" {
		t.Skip("bundled helper not supplied")
	}
	home, err := os.MkdirTemp("", "dv-auto-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	pidFile := filepath.Join(home, "owned.pid")
	wrapper := filepath.Join(home, "helper")
	if err = os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$VMFREE_TEST_PID\"\nexec \"$DOCKIVA_TEST_BUNDLED_HELPER\" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKIVA_VMFREE_HELPER", wrapper)
	t.Setenv("VMFREE_TEST_PID", pidFile)
	t.Setenv("PATH", "/usr/bin:/bin")
	defer func() {
		data, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(string(data))
		if pid > 0 {
			p, _ := os.FindProcess(pid)
			_ = p.Kill()
		}
	}()
	r := vmfreeAt(home)
	defer r.Close()
	if err = startVMFreeEngine(r); err != nil {
		data, _ := os.ReadFile(filepath.Join(home, "dockiva-engine.log"))
		t.Fatalf("%v\n%s", err, data)
	}
	pid, _ := os.ReadFile(pidFile)
	if err = startVMFreeEngine(r); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(pidFile)
	if string(pid) != string(again) {
		t.Fatal("duplicate engine launched")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	versionData, err := r.request(ctx, "GET", "/version", nil)
	var version struct {
		GoVersion  string
		Components []struct{ Name string }
	}
	if err != nil || json.Unmarshal(versionData, &version) != nil || !strings.HasPrefix(version.GoVersion, "go") || len(version.Components) == 0 || version.Components[0].Name != "dockivad" {
		t.Fatalf("expected packaged Go dockivad: %s %v", versionData, err)
	}
	if err = filepath.WalkDir(filepath.Dir(helper), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(entry.Name()), "python") || strings.HasSuffix(path, ".py") || strings.HasSuffix(path, ".pyc") {
			return fmt.Errorf("Python in helper bundle: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// backends lists the acceptable choices: unpinned images run natively (hvf) where
	// Hypervisor.framework is usable and on the interpreter elsewhere.
	assertProfile := func(id string, backends ...string) {
		t.Helper()
		raw, err := r.Inspect(ctx, id)
		var detail struct {
			DockivaCompatibility struct{ Backend, Status, RuntimeABI, ImageDigest, DefinitionDigest string }
		}
		if err != nil || json.Unmarshal(raw, &detail) != nil {
			t.Fatalf("profile inspect: %s %v", raw, err)
		}
		profile := detail.DockivaCompatibility
		if !slices.Contains(backends, profile.Backend) || profile.Status != "EXPERIMENTAL" || profile.RuntimeABI != "dockiva-native-v1" || !strings.HasPrefix(profile.ImageDigest, "sha256:") || !strings.HasPrefix(profile.DefinitionDigest, "sha256:") {
			t.Fatalf("unexpected runtime profile: %s", raw)
		}
	}
	images, err := r.ListRuntimeImages(ctx)
	if err != nil || len(images) != 3 {
		t.Fatalf("seed image: %v %v", images, err)
	}
	item, err := r.CreateRuntimeContainer(ctx, RuntimeCreateContainerRequest{Name: "bundled-test", Image: "vmfree/shell:demo", Command: []string{"-c", "busybox echo bundled"}, AutoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.RemoveContainer(context.Background(), item.ID, true)
	assertProfile(item.ID, "hvf", "interpreter")
	deadline := time.Now().Add(10 * time.Second)
	for {
		logs, err := r.RuntimeReadLogs(ctx, item.ID, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(logs.Data, "bundled") {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(filepath.Join(home, "dockiva-engine.log"))
			t.Fatalf("missing guest output: %s", data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err = r.RestartContainer(ctx, item.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	server, err := r.CreateRuntimeContainer(ctx, RuntimeCreateContainerRequest{Name: "http-demo-test", Image: "vmfree/server:demo", Command: []string{"0"}, AutoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.RemoveContainer(context.Background(), server.ID, true)
	readURL := func() string {
		t.Helper()
		logs, err := r.RuntimeReadLogs(ctx, server.ID, 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(logs.Data)
		if len(fields) != 2 || fields[0] != "PORT" {
			t.Fatalf("missing server port: %q", logs.Data)
		}
		port, err := strconv.Atoi(fields[1])
		if err != nil || port < 1 || port > 65535 {
			t.Fatalf("bad port: %q", logs.Data)
		}
		return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	checkHTTP := func(address string) {
		t.Helper()
		response, err := client.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "Persistent VM-free HTTP server OK") {
			t.Fatalf("HTTP response: %d %s %v", response.StatusCode, body, err)
		}
	}
	// The demo binds a fresh ephemeral port on each start and logs it; after Start or
	// Restart the newest PORT line can lag, so re-read it until the server answers.
	serving := func() string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			address := readURL()
			response, err := client.Get(address)
			if err == nil {
				response.Body.Close()
				checkHTTP(address)
				return address
			}
			if time.Now().After(deadline) {
				t.Fatalf("HTTP demo not serving: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	address := serving()
	time.Sleep(1200 * time.Millisecond)
	rows, err := r.ListContainers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	running := false
	for _, row := range rows {
		if row.ID == server.ID && row.State == "running" {
			running = true
		}
	}
	if !running {
		t.Fatal("HTTP demo did not stay running")
	}
	if err = r.StopContainer(ctx, server.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	if response, err := client.Get(address); err == nil {
		response.Body.Close()
		t.Fatal("HTTP still reachable after stop")
	}
	if err = r.StartContainer(ctx, server.ID); err != nil {
		t.Fatal(err)
	}
	serving()
	if err = r.RestartContainer(ctx, server.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	serving()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addressRedis := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	redis, err := r.CreateRuntimeContainer(ctx, RuntimeCreateContainerRequest{Name: "redis-bundled-test", Image: "vmfree/redis:preview", Command: []string{"--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--dir", "/data", "--save", "", "--appendonly", "yes", "--appendfsync", "always", "--daemonize", "no"}, AutoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.RemoveContainer(context.Background(), redis.ID, true)
	assertProfile(redis.ID, "hvf") // the preset is pinned to native execution
	redisRequest := func(expected string, args ...string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			var conn net.Conn
			for time.Now().Before(deadline) {
				conn, err = net.DialTimeout("tcp", addressRedis, 100*time.Millisecond)
				if err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if err != nil || conn == nil {
				logs, _ := r.RuntimeReadLogs(ctx, redis.ID, 100, 0)
				t.Fatalf("redis connection: %v %s", err, logs.Data)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			fmt.Fprintf(conn, "*%d\r\n", len(args))
			for _, arg := range args {
				fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(arg), arg)
			}
			reader := bufio.NewReader(conn)
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "-LOADING ") && time.Now().Before(deadline) {
				conn.Close()
				time.Sleep(50 * time.Millisecond)
				continue
			}
			var actual string
			if strings.HasPrefix(line, "+") {
				actual = strings.TrimSuffix(line[1:], "\r\n")
			} else if strings.HasPrefix(line, "$") {
				n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
				if err != nil || n < 0 || n > 65536 {
					t.Fatalf("invalid bulk: %q", line)
				}
				data := make([]byte, n+2)
				if _, err = io.ReadFull(reader, data); err != nil {
					t.Fatal(err)
				}
				actual = string(data[:n])
			} else {
				t.Fatalf("unexpected Redis reply %q", line)
			}
			if actual != expected {
				t.Fatalf("%v: got %q want %q", args, actual, expected)
			}
			return
		}
	}
	redisRequest("PONG", "PING")
	redisRequest("OK", "SET", "dockiva-test", "persisted")
	redisRequest("persisted", "GET", "dockiva-test")
	if err = r.StopContainer(ctx, redis.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", addressRedis, 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("Redis listener survived Stop")
	}
	if err = r.StartContainer(ctx, redis.ID); err != nil {
		t.Fatal(err)
	}
	redisRequest("persisted", "GET", "dockiva-test")
	if err = r.RestartContainer(ctx, redis.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	redisRequest("persisted", "GET", "dockiva-test")
}

func TestVMFreeMissingHelperDoesNotChangeRuntime(t *testing.T) {
	t.Setenv("DOCKIVA_VMFREE_HELPER", "/missing/dockiva-test-helper")
	r := vmfreeAt(t.TempDir())
	defer r.Close()
	if err := startVMFreeEngine(r); err == nil {
		t.Fatal("missing helper accepted")
	}
	if r.Info(context.Background()).Provider != "vmfree" {
		t.Fatal("offline identity lost")
	}
}
