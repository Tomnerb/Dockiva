package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type AppSettings struct {
	SettingsVersion     int    `json:"settingsVersion"`
	EngineBackend       string `json:"engineBackend"`
	RuntimeProvider     string `json:"runtimeProvider"`
	DockerEndpoint      string `json:"dockerEndpoint"`
	WSLDistro           string `json:"wslDistro"`
	CloseToTray         bool   `json:"closeToTray"`
	StartAtLogin        bool   `json:"startAtLogin"`
	StartHidden         bool   `json:"startHidden"`
	AutoReconnectEngine bool   `json:"autoReconnectEngine"`
	StartEngineOnLaunch bool   `json:"startEngineOnLaunch"`
}

type AppService struct {
	mu       sync.RWMutex
	settings AppSettings
	path     string
}

func NewAppService() (*AppService, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(configDir, "Dockiva", "settings.json")
	defaults := defaultAppSettings()
	service := &AppService{
		path:     path,
		settings: defaults,
	}

	data, readErr := os.ReadFile(path)
	migratedLegacySettings := false
	if errors.Is(readErr, os.ErrNotExist) {
		legacyPath := filepath.Join(configDir, "DevStack", "settings.json")
		if legacyData, legacyErr := os.ReadFile(legacyPath); legacyErr == nil {
			data = legacyData
			readErr = nil
			migratedLegacySettings = true
		}
	}

	if readErr == nil {
		var versionProbe struct {
			SettingsVersion int `json:"settingsVersion"`
		}
		if err := json.Unmarshal(data, &versionProbe); err != nil {
			return nil, err
		}

		loaded := defaults
		if err := json.Unmarshal(data, &loaded); err != nil {
			return nil, err
		}

		// Settings written before the native-first engine model used "auto" to
		// mean "prefer any reachable Docker endpoint". Preserve that external
		// engine identity during migration instead of unexpectedly changing the
		// user's container store.
		if versionProbe.SettingsVersion == 0 {
			if loaded.EngineBackend == "" || loaded.EngineBackend == "auto" {
				loaded.EngineBackend = "external"
			}
			loaded.SettingsVersion = currentSettingsVersion
		}

		service.settings = mergeAppSettings(defaults, loaded)
		if migratedLegacySettings {
			if err := service.persist(); err != nil {
				return nil, err
			}
		}
	}

	return service, nil
}

const currentSettingsVersion = 2

func defaultAppSettings() AppSettings {
	backend := "native"
	runtimeProvider := "containerd"
	wslDistro := ""

	switch runtime.GOOS {
	case "darwin":
		backend = "vz"
		if runtime.GOARCH == "arm64" {
			backend = "vmfree"
			runtimeProvider = "vmfree"
		}
	case "windows":
		backend = "wsl2"
		runtimeProvider = "docker"
		wslDistro = "Dockiva"
	}

	return AppSettings{
		SettingsVersion:     currentSettingsVersion,
		EngineBackend:       backend,
		RuntimeProvider:     runtimeProvider,
		WSLDistro:           wslDistro,
		CloseToTray:         true,
		AutoReconnectEngine: true,
		// A fresh installation should be usable by opening Dockiva, not by
		// asking the user to run a separate start command. Existing settings are
		// preserved during migration.
		StartEngineOnLaunch: true,
	}
}

func mergeAppSettings(defaults AppSettings, loaded AppSettings) AppSettings {
	if loaded.SettingsVersion == 0 {
		loaded.SettingsVersion = defaults.SettingsVersion
	}
	if loaded.EngineBackend == "" {
		loaded.EngineBackend = defaults.EngineBackend
	}
	loaded.RuntimeProvider = runtimeProviderForBackend(loaded.EngineBackend)
	if runtime.GOOS == "windows" && loaded.WSLDistro == "" {
		loaded.WSLDistro = defaults.WSLDistro
	}
	return loaded
}

func runtimeProviderForBackend(backend string) string {
	backend = normalizeEngineBackend(backend)
	if backend == "vmfree" {
		return "vmfree"
	}
	if backend == "external" || runtime.GOOS == "windows" {
		return "docker"
	}
	return "containerd"
}

func (s *AppService) GetSettings() AppSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *AppService) Snapshot() AppSettings { return s.GetSettings() }

func (s *AppService) UpdateSettings(next AppSettings) error {
	if next.EngineBackend == "" {
		next.EngineBackend = defaultAppSettings().EngineBackend
	}

	switch next.EngineBackend {
	case "auto", "external", "native", "vz", "wsl2", "vmfree":
	default:
		return errors.New("invalid engine backend")
	}

	next.RuntimeProvider = runtimeProviderForBackend(next.EngineBackend)
	next.SettingsVersion = currentSettingsVersion

	switch next.RuntimeProvider {
	case "docker", "containerd", "vmfree":
	default:
		return errors.New("invalid container runtime provider")
	}

	s.mu.Lock()
	s.settings = next
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		return err
	}

	app := application.Get()
	if app == nil {
		return nil
	}

	if next.StartAtLogin {
		return app.Autostart.EnableWithOptions(application.AutostartOptions{
			Identifier: "com.tomnerb.dockiva",
			Arguments:  []string{"--hidden"},
		})
	}

	return app.Autostart.Disable()
}

func (s *AppService) persist() error {
	s.mu.RLock()
	settings := s.settings
	path := s.path
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}
