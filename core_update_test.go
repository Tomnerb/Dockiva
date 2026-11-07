package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestCoreUpdateOutlivesFrontEnd: Wails' swap helper waits for the core's PID and then
// opens the new Dockiva.app with `open -n`. The core used to exit 300 ms after Quit, so
// the new app opened beside the old one. Quit now only marks the update, and the core
// then outlives the front end.
func TestCoreUpdateOutlivesFrontEnd(t *testing.T) {
	dir := os.Getenv("DOCKIVA_TEST_UPDATE_DIR")
	stamp := func(name string) {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o644)
	}
	switch os.Getenv("DOCKIVA_TEST_UPDATE_ROLE") {
	case "frontend": // starts a core, then quits as the app does for an update
		core := exec.Command(os.Args[0], "-test.run=^TestCoreUpdateOutlivesFrontEnd$")
		core.Env = append(os.Environ(), "DOCKIVA_TEST_UPDATE_ROLE=core")
		if core.Start() != nil {
			os.Exit(2)
		}
		time.Sleep(200 * time.Millisecond)
		stamp("frontend")
		os.Exit(0)
	case "core":
		waitForParent(os.Getppid(), 10*time.Second)
		stamp("core")
		os.Exit(0)
	}

	var updating atomic.Bool
	coreUpdateHost{updating: &updating}.Quit()
	if !updating.Load() {
		t.Fatal("Quit did not mark the update")
	}

	dir = t.TempDir()
	frontend := exec.Command(os.Args[0], "-test.run=^TestCoreUpdateOutlivesFrontEnd$")
	frontend.Env = append(os.Environ(), "DOCKIVA_TEST_UPDATE_ROLE=frontend", "DOCKIVA_TEST_UPDATE_DIR="+dir)
	if err := frontend.Run(); err != nil {
		t.Fatal(err)
	}
	read := func(name string) int64 {
		data, _ := os.ReadFile(filepath.Join(dir, name))
		n, _ := strconv.ParseInt(string(data), 10, 64)
		return n
	}
	coreAt := read("core")
	for deadline := time.Now().Add(10 * time.Second); coreAt == 0 && time.Now().Before(deadline); coreAt = read("core") {
		time.Sleep(20 * time.Millisecond)
	}
	if frontAt := read("frontend"); frontAt == 0 || coreAt <= frontAt {
		t.Fatalf("core finished at %d, front end at %d", coreAt, frontAt)
	}
}
