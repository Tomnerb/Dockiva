package main

import (
	"path/filepath"
	"testing"
)

// Uses the real docker CLI against a throwaway DOCKER_CONFIG, so the user's own
// contexts are never touched.
func TestDockivaCLIContextTakesAndGivesBack(t *testing.T) {
	if _, err := dockerCLIPath(); err != nil {
		t.Skip("no docker CLI")
	}
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DOCKER_HOST", "unix:///app/own/setting.sock") // must not leak into the CLI

	run := func(args ...string) string {
		t.Helper()
		out, err := dockerCLI(args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	run("context", "create", "other", "--docker", "host=unix:///tmp/other.sock")
	run("context", "use", "other")
	// A stale context from before the rename gets repaired, not duplicated.
	run("context", "create", dockivaCLIContext, "--docker", "host=unix:///old/.vmfree/docker.sock")

	home := "/tmp/dkv-ctx-test/.dockiva-engine" // the CLI checks the socket path length, not that it exists
	if err := useDockivaCLIContext(home); err != nil {
		t.Fatal(err)
	}
	if got := run("context", "inspect", "--format", "{{.Name}}"); got != dockivaCLIContext {
		t.Fatalf("current context = %q", got)
	}
	want := "unix://" + filepath.Join(home, "docker.sock")
	if got := run("context", "inspect", dockivaCLIContext, "--format", "{{.Endpoints.docker.Host}}"); got != want {
		t.Fatalf("dockiva host = %q, want %q", got, want)
	}
	// Selecting again (every launch) keeps the original context to give back.
	if err := useDockivaCLIContext(home); err != nil {
		t.Fatal(err)
	}

	if err := restoreDockerCLIContext(); err != nil {
		t.Fatal(err)
	}
	if got := run("context", "inspect", "--format", "{{.Name}}"); got != "other" {
		t.Fatalf("restored context = %q, want other", got)
	}
	// Nothing saved any more: a second restore leaves the user's choice alone.
	run("context", "use", dockivaCLIContext)
	if err := restoreDockerCLIContext(); err != nil {
		t.Fatal(err)
	}
	if got := run("context", "inspect", "--format", "{{.Name}}"); got != dockivaCLIContext {
		t.Fatalf("context after second restore = %q", got)
	}
}
