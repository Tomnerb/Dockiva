package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// platformEngineBackend is implemented once per operating system.
//
// The selected implementation is decided by Go build constraints:
//
//	engine_linux.go   -> //go:build linux
//	engine_darwin.go  -> //go:build darwin
//	engine_windows.go -> //go:build windows
//
// Shared UI/service code never needs runtime.GOOS switches for managed-engine
// behavior. Replacing one platform runtime does not affect the other two.
type platformEngineBackend interface {
	Name() string
	Status(service *DockerService, platformOption string) EngineStatus
	Start(service *DockerService, platformOption string) (EngineActionResult, error)
	Stop(service *DockerService, platformOption string) (EngineActionResult, error)
	Delete(service *DockerService, platformOption string) (EngineActionResult, error)
	Provision(service *DockerService, platformOption string) (EngineActionResult, error)
	Options() []string
}

type EngineStatus struct {
	Backend         string   `json:"backend"`
	Platform        string   `json:"platform"`
	Supported       bool     `json:"supported"`
	HelperInstalled bool     `json:"helperInstalled"`
	EngineInstalled bool     `json:"engineInstalled"`
	Running         bool     `json:"running"`
	Endpoint        string   `json:"endpoint,omitempty"`
	Message         string   `json:"message,omitempty"`
	WSLDistros      []string `json:"wslDistros,omitempty"`
}

type EngineActionResult struct {
	Status EngineStatus `json:"status"`
	Output string       `json:"output,omitempty"`
}

func normalizeEngineBackend(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "auto"
	}
	return value
}

func (s *DockerService) GetEngineStatus(
	backend string,
	platformOption string,
) EngineStatus {
	backend = normalizeEngineBackend(backend)
	if backend == "vmfree" {
		r := newVMFreeRuntime()
		defer r.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		info := r.Info(ctx)
		return EngineStatus{Backend: "vmfree", Platform: runtime.GOOS, Supported: runtime.GOOS == "darwin" && runtime.GOARCH == "arm64", Running: info.Connected, EngineInstalled: info.Connected, Endpoint: info.Endpoint, Message: info.Message}
	}

	if backend == "external" {
		return s.externalEngineStatus()
	}

	if backend == "auto" {
		status := s.engine.Status(s, platformOption)
		status.Backend = "auto"
		return status
	}

	if backend != s.engine.Name() {
		return EngineStatus{
			Backend:   backend,
			Platform:  runtime.GOOS,
			Supported: false,
			Message:   "This engine backend is not available in the current OS build.",
		}
	}

	return s.engine.Status(s, platformOption)
}

func (s *DockerService) StartManagedEngine(
	backend string,
	platformOption string,
) (EngineActionResult, error) {
	backend = normalizeEngineBackend(backend)
	if backend == "vmfree" {
		_, err := s.SelectEngineBackend("vmfree", "", "")
		status := s.GetEngineStatus("vmfree", "")
		if err == nil && !status.Running {
			err = errors.New("Dockiva Engine is offline; retry Start Engine")
		}
		return EngineActionResult{Status: status}, err
	}

	if backend == "auto" {
		return s.engine.Start(s, platformOption)
	}

	if backend == "external" {
		err := s.ReconnectExternalDocker()
		return EngineActionResult{
			Status: s.externalEngineStatus(),
		}, err
	}

	if backend != s.engine.Name() {
		return EngineActionResult{
			Status: s.GetEngineStatus(backend, platformOption),
		}, errors.New("selected engine backend is unavailable on this operating system")
	}

	return s.engine.Start(s, platformOption)
}

// SelectEngineBackend keeps the top-level engine choice and active runtime in
// sync. A stopped native engine deliberately installs an offline runtime so a
// previously connected external Docker daemon cannot leak into the UI.
func (s *DockerService) SelectEngineBackend(
	backend string,
	platformOption string,
	externalEndpoint string,
) (string, error) {
	backend = normalizeEngineBackend(backend)
	if backend == "vmfree" {
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			return "", errors.New("Dockiva Engine requires an Apple Silicon Mac")
		}
		// Keep this identity even while offline; never expose the previous Docker store.
		r := newVMFreeRuntime()
		s.replaceRuntime(r)
		return "vmfree", startVMFreeEngine(r)
	}
	if backend == "auto" {
		backend = s.engine.Name()
	}

	if backend == "external" {
		externalEndpoint = resolveExternalDockerEndpoint(externalEndpoint)
		if strings.TrimSpace(externalEndpoint) != "" {
			if err := s.ConfigureDockerEndpoint(externalEndpoint); err != nil {
				return "", err
			}
		}
		// Connect immediately when the endpoint is already available. An
		// offline external engine remains selectable so the UI can offer an
		// explicit reconnect/start action without launching it during startup.
		_ = s.ReconnectExternalDocker()
		if err := s.SelectContainerRuntime("docker"); err != nil {
			return "", err
		}
		return "docker", nil
	}

	if backend != s.engine.Name() {
		return "", errors.New("selected engine backend is unavailable on this operating system")
	}

	status := s.engine.Status(s, platformOption)
	if !status.Running {
		return s.useOfflineNativeRuntime(status), nil
	}

	provider := nativeRuntimeProvider()
	if provider == "docker" && strings.TrimSpace(status.Endpoint) != "" {
		if err := s.ConfigureNativeDockerEndpoint(status.Endpoint); err != nil {
			return "", err
		}
	}
	if err := s.SelectContainerRuntime(provider); err != nil {
		return "", err
	}
	return provider, nil
}

func (s *DockerService) StopManagedEngine(
	backend string,
	platformOption string,
) (EngineActionResult, error) {
	backend = normalizeEngineBackend(backend)

	if backend == "auto" {
		backend = s.engine.Name()
	}

	if backend == "external" {
		return EngineActionResult{
			Status: s.externalEngineStatus(),
		}, errors.New("Dockiva will not stop an externally managed Docker service")
	}

	if backend != s.engine.Name() {
		return EngineActionResult{
			Status: s.GetEngineStatus(backend, platformOption),
		}, errors.New("selected engine backend is unavailable on this operating system")
	}

	return s.engine.Stop(s, platformOption)
}

func (s *DockerService) DeleteManagedEngine(
	backend string,
	platformOption string,
) (EngineActionResult, error) {
	backend = normalizeEngineBackend(backend)

	if backend == "auto" {
		backend = s.engine.Name()
	}

	if backend == "external" {
		return EngineActionResult{
			Status: s.externalEngineStatus(),
		}, errors.New("external Docker is not owned by Dockiva")
	}

	if backend != s.engine.Name() {
		return EngineActionResult{
			Status: s.GetEngineStatus(backend, platformOption),
		}, errors.New("selected engine backend is unavailable on this operating system")
	}

	return s.engine.Delete(s, platformOption)
}

// ProvisionWSLEngine is intentionally kept as a stable Wails binding so the
// current frontend does not need OS-specific generated bindings. On non-Windows
// builds the platform backend returns a clear unsupported error.
func (s *DockerService) ProvisionWSLEngine(
	platformOption string,
) (EngineActionResult, error) {
	return s.engine.Provision(s, platformOption)
}

// ListWSLDistros keeps the existing API stable. It returns an empty list on
// platforms whose engine backend has no distro selector.
func (s *DockerService) ListWSLDistros() []string {
	return s.engine.Options()
}

func (s *DockerService) externalEngineStatus() EngineStatus {
	status := EngineStatus{
		Backend:   "external",
		Platform:  runtime.GOOS,
		Supported: true,
		Message:   "Uses DOCKER_HOST or the active Docker CLI context.",
	}

	status.Endpoint =
		s.ConfiguredDockerEndpoint()

	cli := s.client.Load()
	if cli == nil {
		return status
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := cli.Ping(ctx, client.PingOptions{
		NegotiateAPIVersion: true,
	}); err == nil {
		status.Running = true
		status.EngineInstalled = true
		status.Endpoint = strings.TrimSpace(os.Getenv("DOCKER_HOST"))

		if status.Endpoint == "" {
			if host, _, err := activeDockerContext(); err == nil {
				status.Endpoint = host
			}
		}

		status.Message = "Docker API is reachable."
	}

	return status
}

// SwitchDockerEndpoint verifies a new endpoint before atomically making it the
// active client. The previous client remains alive until application shutdown
// so an in-flight request is not invalidated by an engine switch.
func (s *DockerService) SwitchDockerEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("Docker endpoint is empty")
	}

	next, err := client.New(
		client.WithHost(endpoint),
		client.WithUserAgent("dockiva/0.9.0"),
	)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	if _, err := next.Ping(ctx, client.PingOptions{
		NegotiateAPIVersion: true,
	}); err != nil {
		_ = next.Close()
		return err
	}

	s.replaceDockerClient(next, endpoint)
	return nil
}

// ConfigureDockerEndpoint binds the Docker client to the saved engine identity
// without requiring that engine to be online yet.
func (s *DockerService) ConfigureDockerEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("Docker endpoint is empty")
	}

	next, err := client.New(
		client.WithHost(endpoint),
		client.WithUserAgent("dockiva/0.9.0"),
	)
	if err != nil {
		return err
	}
	s.replaceDockerClient(next, endpoint)
	return nil
}

// ConfigureNativeDockerEndpoint selects the Docker API exposed by a
// Dockiva-owned VM without overwriting the user's saved External Docker
// identity. This keeps Docker Desktop and Dockiva Native separate stores.
func (s *DockerService) ConfigureNativeDockerEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("native Docker endpoint is empty")
	}
	if strings.HasPrefix(endpoint, "/") {
		endpoint = "unix://" + endpoint
	}
	next, err := client.New(client.WithHost(endpoint), client.WithUserAgent("dockiva/0.9.0"))
	if err != nil {
		return err
	}
	previous := s.client.Swap(next)
	if previous != nil {
		s.retiredClientMu.Lock()
		s.retiredClients = append(s.retiredClients, previous)
		s.retiredClientMu.Unlock()
	}
	return nil
}

func (s *DockerService) replaceDockerClient(next *client.Client, endpoint string) {
	previous := s.client.Swap(next)
	if previous != nil {
		s.retiredClientMu.Lock()
		s.retiredClients = append(s.retiredClients, previous)
		s.retiredClientMu.Unlock()
	}
	_ = os.Setenv("DOCKER_HOST", endpoint)
	s.SetConfiguredDockerEndpoint(endpoint)
}

func (s *DockerService) ReconnectExternalDocker() error {
	return s.ReconnectConfiguredDocker()
}

func (s *DockerService) RecoverDockerConnection() (
	EngineActionResult,
	error,
) {
	err := s.ReconnectConfiguredDocker()
	if err != nil {
		started, startErr := startExternalDockerOnRequest(
			s.ConfiguredDockerEndpoint(),
		)
		if started {
			err = startErr
			if err == nil {
				err = s.ReconnectConfiguredDocker()
			}
		}
	}

	status := s.externalEngineStatus()
	endpoint := s.ConfiguredDockerEndpoint()
	status.Endpoint = endpoint

	if err == nil {
		status.Message =
			"Reconnected to " +
				dockerEndpointDisplayName(
					endpoint,
				) +
				" at " +
				endpoint
	}

	return EngineActionResult{
		Status: status,
	}, err
}

func (s *DockerService) DockerEndpointIdentity() map[string]string {
	endpoint :=
		s.ConfiguredDockerEndpoint()

	return map[string]string{
		"endpoint": endpoint,
		"kind": dockerEndpointKind(
			endpoint,
		),
		"displayName": dockerEndpointDisplayName(
			endpoint,
		),
	}
}

func runCommand(
	timeout time.Duration,
	dir string,
	executable string,
	args ...string,
) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, executable, args...)

	if dir != "" {
		cmd.Dir = filepath.Clean(dir)
	}

	cmd.Env = os.Environ()

	outputBytes, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(outputBytes))

	if ctx.Err() == context.DeadlineExceeded {
		return output, errors.New("command timed out")
	}

	if err != nil {
		if output != "" {
			return output, errors.New(output)
		}
		return "", err
	}

	return output, nil
}
