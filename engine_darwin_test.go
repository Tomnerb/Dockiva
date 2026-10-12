//go:build darwin

package main

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStageCompressedBundledGuestAssets(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	t.Setenv("DOCKIVA_GUEST_ASSETS_PATH", source)
	t.Setenv("DOCKIVA_GUEST_DIR", destination)

	if err := os.WriteFile(filepath.Join(source, "vmlinux"), []byte("kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	disk, err := os.Create(filepath.Join(source, "rootfs.ext4.gz"))
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(disk)
	if _, err := compressed.Write([]byte("compressed disk")); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}

	if err := stageBundledGuestAssets(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "rootfs.ext4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "compressed disk" {
		t.Fatalf("rootfs.ext4 = %q, want decompressed disk", data)
	}
}

func TestStageBundledGuestAssets(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	t.Setenv("DOCKIVA_GUEST_ASSETS_PATH", source)
	t.Setenv("DOCKIVA_GUEST_DIR", destination)

	for name, content := range map[string]string{
		"vmlinux":      "kernel",
		"rootfs.ext4":  "disk",
		"manifest.txt": "manifest",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := stageBundledGuestAssets(); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"vmlinux":      "kernel",
		"rootfs.ext4":  "disk",
		"manifest.txt": "manifest",
	} {
		data, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", name, data, want)
		}
	}

	if err := os.WriteFile(filepath.Join(destination, "rootfs.ext4"), []byte("persistent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stageBundledGuestAssets(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "rootfs.ext4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "persistent" {
		t.Fatalf("existing guest disk was overwritten: %q", data)
	}
}

func TestDockivaVMMStateDirReusesRunningLegacyVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacy := filepath.Join(home, "Library", "Application Support", "DevStack", "run")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if err := os.WriteFile(filepath.Join(legacy, "vmm.pid"), []byte(fmt.Sprint(pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	status := fmt.Sprintf(`{"running":true,"pid":%d}`, pid)
	if err := os.WriteFile(filepath.Join(legacy, "status.json"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := dockivaVMMStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Fatalf("state dir = %q, want running legacy state %q", got, legacy)
	}
}

func TestDockivaVMMStateDirIgnoresStaleLegacyVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, "Library", "Application Support", "DevStack", "run")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "vmm.pid"), []byte("99999999"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "status.json"), []byte(`{"running":true,"pid":99999999}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := dockivaVMMStateDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Library", "Application Support", "Dockiva", "run")
	if got != want {
		t.Fatalf("state dir = %q, want current state %q", got, want)
	}
}

// A guest whose Docker never answers (for example a kernel panic at boot) makes
// Start fail and stop the VM, instead of leaving it spinning in the background.
func TestNativeVMStartStopsGuestThatNeverAnswers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	guest := filepath.Join(home, "guest")
	if err := os.MkdirAll(guest, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vmlinux", "rootfs.ext4"} {
		if err := os.WriteFile(filepath.Join(guest, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DOCKIVA_GUEST_DIR", guest)

	// serve --kernel K --disk D --state-dir S ...: a plain docker.sock file that
	// nothing answers on; stop --state-dir S: record the call and end serve.
	stopped := filepath.Join(home, "stopped")
	helper := filepath.Join(home, "dockiva-vmm")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
serve) : > "$7/docker.sock"; echo $$ > "$7/vmm.pid"; exec sleep 30 ;;
stop) touch %q; kill "$(cat "$3/vmm.pid")" 2>/dev/null; exit 0 ;;
status) echo '{"running":false}' ;;
esac
`, stopped)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKIVA_VMM_PATH", helper)

	previous := nativeVMReadyTimeout
	nativeVMReadyTimeout = 2 * time.Second
	defer func() { nativeVMReadyTimeout = previous }()

	_, err := darwinEngineBackend{}.Start(nil, "")
	if err == nil {
		t.Fatal("Start succeeded although Docker never answered")
	}
	if !fileExists(stopped) {
		t.Fatalf("Start did not stop the unready VM: %v", err)
	}
}
