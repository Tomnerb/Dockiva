package main

import (
	"runtime"
	"testing"
)

func TestDefaultAppSettingsPreferNativeEngine(t *testing.T) {
	settings := defaultAppSettings()

	wantBackend := "native"
	wantRuntime := "containerd"
	switch runtime.GOOS {
	case "darwin":
		wantBackend = "vz"
		if runtime.GOARCH == "arm64" {
			wantBackend = "vmfree"
			wantRuntime = "vmfree"
		}
	case "windows":
		wantBackend = "wsl2"
		wantRuntime = "docker"
	}

	if settings.EngineBackend != wantBackend {
		t.Fatalf("default backend = %q, want %q", settings.EngineBackend, wantBackend)
	}
	if settings.RuntimeProvider != wantRuntime {
		t.Fatalf("default runtime = %q, want %q", settings.RuntimeProvider, wantRuntime)
	}
	if settings.SettingsVersion != currentSettingsVersion {
		t.Fatalf("settings version = %d, want %d", settings.SettingsVersion, currentSettingsVersion)
	}
	if !settings.StartEngineOnLaunch {
		t.Fatal("new installs must start the selected native engine automatically")
	}
}

func TestRuntimeProviderFollowsEngineIdentity(t *testing.T) {
	if got := runtimeProviderForBackend("external"); got != "docker" {
		t.Fatalf("external runtime = %q, want docker", got)
	}

	wantNative := "containerd"
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		wantNative = "vmfree"
	}
	if runtime.GOOS == "windows" {
		wantNative = "docker"
	}
	if got := runtimeProviderForBackend(defaultAppSettings().EngineBackend); got != wantNative {
		t.Fatalf("native runtime = %q, want %q", got, wantNative)
	}
}
