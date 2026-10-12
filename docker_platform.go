package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const dockerChangedEvent = "dockiva:docker-event"

type PlatformInfo struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	DockerCLI     string `json:"dockerCli,omitempty"`
	DockerContext string `json:"dockerContext,omitempty"`
	DockerHost    string `json:"dockerHost,omitempty"`
	HostSource    string `json:"hostSource,omitempty"`
}

type DockerEventNotice struct {
	Type   string `json:"type,omitempty"`
	Action string `json:"action,omitempty"`
	ID     string `json:"id,omitempty"`
	Time   int64  `json:"time,omitempty"`
}

type ComposeImportResult struct {
	Project     string   `json:"project"`
	WorkingDir  string   `json:"workingDir"`
	ConfigFiles []string `json:"configFiles"`
	Output      string   `json:"output"`
}

type dockerContextInspect struct {
	Name      string `json:"Name"`
	Endpoints struct {
		Docker struct {
			Host string `json:"Host"`
		} `json:"docker"`
	} `json:"Endpoints"`
}

func newDockerAPIClient() (*client.Client, error) {
	ensureDockerCLIPath()

	opts := []client.Opt{
		client.FromEnv,
		client.WithUserAgent("dockiva/0.7.0"),
	}

	// Explicit DOCKER_HOST always wins. If it is not set, use the active
	// Docker CLI context for local Unix-socket/named-pipe contexts. This makes
	// Docker Desktop work naturally on macOS and Windows without hard-coding
	// Linux's /var/run/docker.sock.
	if strings.TrimSpace(os.Getenv("DOCKER_HOST")) == "" {
		if host, _, err := activeDockerContext(); err == nil && isLocalDockerHost(host) {
			opts = append(opts, client.WithHost(host))
		}
	}

	return client.New(opts...)
}

func preferredDockerEndpoint() string {
	if explicit := strings.TrimSpace(
		os.Getenv("DOCKER_HOST"),
	); explicit != "" {
		return explicit
	}

	host, _, err := activeDockerContext()
	if err == nil {
		return strings.TrimSpace(host)
	}

	return ""
}

// resolveExternalDockerEndpoint repairs a saved local endpoint only when its
// socket has disappeared and the Docker CLI now identifies a different local
// context. A healthy saved daemon and remote endpoints are never replaced.
func resolveExternalDockerEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || !missingLocalDockerSocket(endpoint) {
		return endpoint
	}

	preferred := strings.TrimSpace(preferredDockerEndpoint())
	if preferred != "" && isLocalDockerHost(preferred) &&
		!sameDockerEndpoint(endpoint, preferred) {
		return preferred
	}

	return endpoint
}

func missingLocalDockerSocket(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if !strings.HasPrefix(strings.ToLower(endpoint), "unix://") {
		return false
	}
	path := strings.TrimPrefix(endpoint, "unix://")
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

func dockerEndpointKind(
	endpoint string,
) string {
	endpoint = strings.ToLower(
		strings.TrimSpace(endpoint),
	)

	switch {
	case strings.Contains(
		endpoint,
		"/.docker/desktop/docker.sock",
	):
		return "docker-desktop"

	case strings.Contains(
		endpoint,
		"/.docker/run/docker.sock",
	):
		return "docker-desktop"

	case endpoint ==
		"unix:///var/run/docker.sock",
		endpoint ==
			"unix:///run/docker.sock":
		return "system-docker"

	case strings.Contains(
		endpoint,
		"/run/user/",
	) &&
		strings.HasSuffix(
			endpoint,
			"/docker.sock",
		):
		return "rootless-docker"

	case strings.HasPrefix(
		endpoint,
		"npipe:",
	):
		return "windows-docker"

	default:
		return "external"
	}
}

func dockerEndpointDisplayName(
	endpoint string,
) string {
	switch dockerEndpointKind(endpoint) {
	case "docker-desktop":
		return "Docker Desktop"

	case "system-docker":
		return "System Docker Engine"

	case "rootless-docker":
		return "Rootless Docker"

	case "windows-docker":
		return "Docker Engine"

	default:
		return "External Docker"
	}
}

func (s *DockerService) SetConfiguredDockerEndpoint(
	endpoint string,
) {
	s.dockerEndpointMu.Lock()
	s.dockerEndpoint =
		strings.TrimSpace(endpoint)
	s.dockerEndpointMu.Unlock()
}

func (s *DockerService) ConfiguredDockerEndpoint() string {
	s.dockerEndpointMu.RLock()
	endpoint := s.dockerEndpoint
	s.dockerEndpointMu.RUnlock()

	if strings.TrimSpace(endpoint) != "" {
		return endpoint
	}

	return preferredDockerEndpoint()
}

func (s *DockerService) ReconnectConfiguredDocker() error {
	endpoint :=
		s.ConfiguredDockerEndpoint()

	if endpoint == "" {
		return errors.New(
			"no configured Docker endpoint is available",
		)
	}

	if !reachableDockerEndpoint(endpoint) {
		return fmt.Errorf(
			"%s is offline at %s",
			dockerEndpointDisplayName(
				endpoint,
			),
			endpoint,
		)
	}

	return s.SwitchDockerEndpoint(
		endpoint,
	)
}

func dockerEndpointCandidates() []string {
	var candidates []string

	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}

		for _, existing := range candidates {
			if sameDockerEndpoint(existing, value) {
				return
			}
		}

		candidates = append(candidates, value)
	}

	// Respect an explicit endpoint first, but do not stop recovery if it is
	// stale or offline.
	add(os.Getenv("DOCKER_HOST"))

	// The active CLI context is useful when it is healthy. Docker Desktop can
	// leave this pointing at a socket that no longer exists, so it is only one
	// candidate rather than the single source of truth.
	if host, _, err := activeDockerContext(); err == nil {
		add(host)
	}

	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "linux":
		add("unix:///var/run/docker.sock")
		add("unix:///run/docker.sock")

		if runtimeDir := strings.TrimSpace(
			os.Getenv("XDG_RUNTIME_DIR"),
		); runtimeDir != "" {
			add(
				"unix://" +
					filepath.Join(
						runtimeDir,
						"docker.sock",
					),
			)
		}

		if home != "" {
			add(
				"unix://" +
					filepath.Join(
						home,
						".docker",
						"run",
						"docker.sock",
					),
			)

			// Keep Docker Desktop as a low-priority candidate. It is ignored
			// automatically when the socket is absent/offline.
			add(
				"unix://" +
					filepath.Join(
						home,
						".docker",
						"desktop",
						"docker.sock",
					),
			)
		}

	case "darwin":
		add("unix:///var/run/docker.sock")

		if home != "" {
			add(
				"unix://" +
					filepath.Join(
						home,
						".docker",
						"run",
						"docker.sock",
					),
			)

			add(
				"unix://" +
					filepath.Join(
						home,
						".colima",
						"default",
						"docker.sock",
					),
			)

			add(
				"unix://" +
					filepath.Join(
						home,
						".docker",
						"desktop",
						"docker.sock",
					),
			)

			add(
				"unix://" +
					filepath.Join(
						home,
						".orbstack",
						"run",
						"docker.sock",
					),
			)
		}

	case "windows":
		add("npipe:////./pipe/docker_engine")
	}

	return candidates
}

func sameDockerEndpoint(a string, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)

	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}

	return a == b
}

func reachableDockerEndpoint(
	endpoint string,
) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}

	probe, err := client.New(
		client.WithHost(endpoint),
		client.WithUserAgent("dockiva/0.16.1"),
	)
	if err != nil {
		return false
	}
	defer probe.Close()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		1200*time.Millisecond,
	)
	defer cancel()

	_, err = probe.Ping(
		ctx,
		client.PingOptions{
			NegotiateAPIVersion: true,
		},
	)

	return err == nil
}

func (s *DockerService) DiscoverDockerEndpoint() (
	string,
	error,
) {
	var tried []string

	for _, endpoint := range dockerEndpointCandidates() {
		tried = append(
			tried,
			endpoint,
		)

		if reachableDockerEndpoint(endpoint) {
			return endpoint, nil
		}
	}

	if len(tried) == 0 {
		return "", errors.New(
			"no local Docker endpoints were discovered",
		)
	}

	return "", fmt.Errorf(
		"no reachable Docker API endpoint found; tried: %s",
		strings.Join(tried, ", "),
	)
}

func isLocalDockerHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.HasPrefix(host, "unix://") ||
		strings.HasPrefix(host, "npipe://")
}

func ensureDockerCLIPath() {
	if _, err := exec.LookPath(dockerExecutableName()); err == nil {
		return
	}

	var candidates []string

	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/opt/homebrew/bin/docker",
			"/usr/local/bin/docker",
			"/Applications/Docker.app/Contents/Resources/bin/docker",
		}

	case "windows":
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			candidates = append(
				candidates,
				filepath.Join(
					programFiles,
					"Docker",
					"Docker",
					"resources",
					"bin",
					"docker.exe",
				),
			)
		}

	case "linux":
		candidates = []string{
			"/usr/bin/docker",
			"/usr/local/bin/docker",
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "docker"),
		}
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}

		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}

		dir := filepath.Dir(candidate)
		currentPath := os.Getenv("PATH")
		separator := string(os.PathListSeparator)

		if !pathContains(currentPath, dir) {
			_ = os.Setenv("PATH", dir+separator+currentPath)
		}

		return
	}
}

func pathContains(pathValue string, dir string) bool {
	for _, item := range filepath.SplitList(pathValue) {
		if samePath(item, dir) {
			return true
		}
	}
	return false
}

func samePath(a string, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)

	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}

	return a == b
}

func dockerExecutableName() string {
	if runtime.GOOS == "windows" {
		return "docker.exe"
	}
	return "docker"
}

func dockerCLIPath() (string, error) {
	ensureDockerCLIPath()
	return exec.LookPath(dockerExecutableName())
}

func activeDockerContext() (host string, name string, err error) {
	dockerPath, err := dockerCLIPath()
	if err != nil {
		return "", "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, dockerPath, "context", "inspect")
	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}

	var contexts []dockerContextInspect
	if err := json.Unmarshal(output, &contexts); err != nil {
		return "", "", err
	}

	if len(contexts) == 0 {
		return "", "", errors.New("docker context inspect returned no contexts")
	}

	return strings.TrimSpace(contexts[0].Endpoints.Docker.Host),
		strings.TrimSpace(contexts[0].Name),
		nil
}

func (s *DockerService) GetPlatformInfo() PlatformInfo {
	ensureDockerCLIPath()

	info := PlatformInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	if path, err := dockerCLIPath(); err == nil {
		info.DockerCLI = path
	}

	if explicit := strings.TrimSpace(os.Getenv("DOCKER_HOST")); explicit != "" {
		info.DockerHost = explicit
		info.HostSource = "DOCKER_HOST"
	} else if host, name, err := activeDockerContext(); err == nil {
		info.DockerHost = host
		info.DockerContext = name
		info.HostSource = "docker context"
	}

	return info
}

// ServiceStartup starts a lightweight Docker event watcher. Failure to start
// the watcher is non-fatal: Dockiva can still operate through normal refreshes.
func (s *DockerService) ServiceStartup(
	ctx context.Context,
	options application.ServiceOptions,
) error {
	ensureDockerCLIPath()

	go s.watchDockerEvents(ctx)

	return nil
}

func (s *DockerService) watchDockerEvents(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		dockerPath, err := dockerCLIPath()
		if err != nil {
			if !sleepContext(ctx, 3*time.Second) {
				return
			}
			continue
		}

		cmd := exec.CommandContext(
			ctx,
			dockerPath,
			"events",
			"--format",
			"{{json .}}",
		)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			if !sleepContext(ctx, 2*time.Second) {
				return
			}
			continue
		}

		if err := cmd.Start(); err != nil {
			if !sleepContext(ctx, 2*time.Second) {
				return
			}
			continue
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		for scanner.Scan() {
			if ctx.Err() != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return
			}

			line := scanner.Bytes()

			var raw struct {
				Type   string `json:"Type"`
				Action string `json:"Action"`
				ID     string `json:"id"`
				Time   int64  `json:"time"`
			}

			if err := json.Unmarshal(line, &raw); err != nil {
				continue
			}

			if app := application.Get(); app != nil {
				app.Event.Emit(
					dockerChangedEvent,
					DockerEventNotice{
						Type:   raw.Type,
						Action: raw.Action,
						ID:     raw.ID,
						Time:   raw.Time,
					},
				)
			}
		}

		_ = cmd.Wait()

		if !sleepContext(ctx, 1500*time.Millisecond) {
			return
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *DockerService) SelectComposeFile() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("application is not ready")
	}

	return app.Dialog.OpenFile().
		SetTitle("Select Docker Compose file").
		AddFilter(
			"Docker Compose",
			"compose.yml;compose.yaml;docker-compose.yml;docker-compose.yaml;*.yml;*.yaml",
		).
		AddFilter("All Files", "*.*").
		PromptForSingleSelection()
}

func (s *DockerService) SelectComposeFolder() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("application is not ready")
	}

	return app.Dialog.OpenFile().
		SetTitle("Select Docker Compose project folder").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
}

func (s *DockerService) ImportComposePath(inputPath string) (ComposeImportResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return ComposeImportResult{}, errors.New("Compose unsupported by VM-free preview")
	}

	inputPath = strings.TrimSpace(inputPath)
	if inputPath == "" {
		return ComposeImportResult{}, errors.New("compose path is required")
	}

	absolutePath, err := filepath.Abs(inputPath)
	if err != nil {
		return ComposeImportResult{}, err
	}

	configFiles, workingDir, err := discoverComposeFiles(absolutePath)
	if err != nil {
		return ComposeImportResult{}, err
	}

	project := composeProjectName(filepath.Base(workingDir))
	if project == "" {
		project = "dockiva"
	}

	args := []string{
		"compose",
		"--ansi", "never",
		"-p", project,
		"--project-directory", workingDir,
	}

	for _, configFile := range configFiles {
		args = append(args, "-f", configFile)
	}

	args = append(args, "up", "-d")

	output, err := runDockerCLI(30*time.Minute, workingDir, args...)
	if err != nil {
		return ComposeImportResult{
			Project:     project,
			WorkingDir:  workingDir,
			ConfigFiles: configFiles,
			Output:      output,
		}, err
	}

	return ComposeImportResult{
		Project:     project,
		WorkingDir:  workingDir,
		ConfigFiles: configFiles,
		Output:      output,
	}, nil
}

func discoverComposeFiles(inputPath string) ([]string, string, error) {
	info, err := os.Stat(inputPath)
	if err != nil {
		return nil, "", err
	}

	if !info.IsDir() {
		ext := strings.ToLower(filepath.Ext(inputPath))
		if ext != ".yml" && ext != ".yaml" {
			return nil, "", fmt.Errorf(
				"%q is not a YAML Docker Compose file",
				inputPath,
			)
		}

		return []string{filepath.Clean(inputPath)},
			filepath.Dir(inputPath),
			nil
	}

	workingDir := filepath.Clean(inputPath)

	baseCandidates := []string{
		"compose.yaml",
		"compose.yml",
		"docker-compose.yaml",
		"docker-compose.yml",
	}

	var base string
	for _, candidate := range baseCandidates {
		path := filepath.Join(workingDir, candidate)
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			base = path
			break
		}
	}

	if base == "" {
		return nil, "", fmt.Errorf(
			"no compose.yaml, compose.yml, docker-compose.yaml, or docker-compose.yml found in %q",
			workingDir,
		)
	}

	files := []string{base}

	overrideCandidates := []string{
		"compose.override.yaml",
		"compose.override.yml",
		"docker-compose.override.yaml",
		"docker-compose.override.yml",
	}

	for _, candidate := range overrideCandidates {
		path := filepath.Join(workingDir, candidate)
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			files = append(files, path)
		}
	}

	sort.Strings(files[1:])

	return files, workingDir, nil
}

func composeProjectName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))

	var builder strings.Builder

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}

	result := strings.Trim(builder.String(), "-_")

	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	return result
}
