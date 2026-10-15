package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moby/moby/client"
)

const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"

	terminalOutputEvent = "dockiva:terminal-output"
	logOutputEvent      = "dockiva:log-output"
)

type terminalSession struct {
	id          string
	containerID string
	execID      string
	response    client.ExecAttachResult
	closed      atomic.Bool
	seq         atomic.Uint64
}

type logStreamSession struct {
	id          string
	containerID string
	cancel      context.CancelFunc
	closed      atomic.Bool
	seq         atomic.Uint64
}

type DockerService struct {
	client atomic.Pointer[client.Client]

	engine platformEngineBackend

	runtimeMu sync.RWMutex
	runtime   ContainerRuntime

	retiredClientMu sync.Mutex
	retiredClients  []*client.Client

	dockerEndpointMu sync.RWMutex
	dockerEndpoint   string

	sessionMu        sync.Mutex
	terminalSessions map[string]*terminalSession
	logStreams       map[string]*logStreamSession

	migrationMu      sync.RWMutex
	migrationStatus  DockerMigrationStatus
	migrationStarted time.Time
}

type DockerStatus struct {
	Connected  bool   `json:"connected"`
	APIVersion string `json:"apiVersion"`
	OSType     string `json:"osType"`
	Error      string `json:"error,omitempty"`
}

type PortInfo struct {
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort"`
	Type        string `json:"type"`
	Display     string `json:"display"`
	URL         string `json:"url,omitempty"`
}

type ContainerInfo struct {
	ID             string     `json:"id"`
	ShortID        string     `json:"shortId"`
	Name           string     `json:"name"`
	Image          string     `json:"image"`
	State          string     `json:"state"`
	Status         string     `json:"status"`
	ComposeProject string     `json:"composeProject,omitempty"`
	ComposeService string     `json:"composeService,omitempty"`
	IPAddress      string     `json:"ipAddress,omitempty"`
	Ports          []PortInfo `json:"ports"`
}

type ContainerResourceStats struct {
	ID            string  `json:"id"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsage   uint64  `json:"memoryUsage"`
	MemoryLimit   uint64  `json:"memoryLimit"`
	MemoryPercent float64 `json:"memoryPercent"`
	PIDs          uint64  `json:"pids"`
	Error         string  `json:"error,omitempty"`
}

type ImageInfo struct {
	ID         string   `json:"id"`
	ShortID    string   `json:"shortId"`
	Tags       []string `json:"tags"`
	Size       int64    `json:"size"`
	Created    int64    `json:"created"`
	Containers int64    `json:"containers"`
}

type VolumeInfo struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Mountpoint string            `json:"mountpoint"`
	CreatedAt  string            `json:"createdAt,omitempty"`
	Labels     map[string]string `json:"labels"`
}

type NetworkInfo struct {
	ID         string `json:"id"`
	ShortID    string `json:"shortId"`
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Scope      string `json:"scope"`
	Internal   bool   `json:"internal"`
	Attachable bool   `json:"attachable"`
	Ingress    bool   `json:"ingress"`
}

type StreamOutputEvent struct {
	StreamID    string `json:"streamId"`
	ContainerID string `json:"containerId"`
	Data        string `json:"data,omitempty"`
	Error       string `json:"error,omitempty"`
	Closed      bool   `json:"closed,omitempty"`
	Seq         uint64 `json:"seq"`
}

type dockerStatsPayload struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`

	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`

	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`

	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type rawImageSummary struct {
	ID         string   `json:"Id"`
	RepoTags   []string `json:"RepoTags"`
	Created    int64    `json:"Created"`
	Size       int64    `json:"Size"`
	Containers int64    `json:"Containers"`
}

type rawVolume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Scope      string            `json:"Scope"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
}

type rawNetwork struct {
	ID         string `json:"Id"`
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Scope      string `json:"Scope"`
	Internal   bool   `json:"Internal"`
	Attachable bool   `json:"Attachable"`
	Ingress    bool   `json:"Ingress"`
}

func NewDockerService() (*DockerService, error) {
	cli, err := newDockerAPIClient()
	if err != nil {
		return nil, err
	}

	service := &DockerService{
		engine:           newPlatformEngineBackend(),
		terminalSessions: make(map[string]*terminalSession),
		logStreams:       make(map[string]*logStreamSession),
	}
	service.client.Store(cli)
	service.runtime = newDockerRuntime(service)
	service.SetConfiguredDockerEndpoint(
		preferredDockerEndpoint(),
	)

	return service, nil
}

func (s *DockerService) Status() DockerStatus {
	if r, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		info := r.Info(ctx)
		status := DockerStatus{Connected: info.Connected, APIVersion: "1.45", OSType: "linux"}
		if !info.Connected {
			status.Error = info.Message
		}
		return status
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ping, err := s.client.Load().Ping(ctx, client.PingOptions{
		NegotiateAPIVersion: true,
	})
	if err != nil {
		return DockerStatus{
			Connected: false,
			Error:     err.Error(),
		}
	}

	return DockerStatus{
		Connected:  true,
		APIVersion: ping.APIVersion,
		OSType:     ping.OSType,
	}
}

func (s *DockerService) ListContainers() ([]ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	return s.currentRuntime().ListContainers(ctx)
}

func (s *DockerService) ListImages() ([]ImageInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if runtime, ok := s.currentRuntime().(runtimeImageLister); ok {
		return runtime.ListRuntimeImages(ctx)
	}

	result, err := s.client.Load().ImageList(
		ctx,
		client.ImageListOptions{
			All: false,
		},
	)
	if err != nil {
		return nil, err
	}

	images := make([]ImageInfo, 0, len(result.Items))

	for _, item := range result.Items {
		rawBytes, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}

		var raw rawImageSummary
		if err := json.Unmarshal(rawBytes, &raw); err != nil {
			return nil, err
		}

		tags := raw.RepoTags
		if len(tags) == 0 {
			tags = []string{"<none>:<none>"}
		}

		images = append(images, ImageInfo{
			ID:         raw.ID,
			ShortID:    strings.TrimPrefix(shortenID(raw.ID), "sha256:"),
			Tags:       tags,
			Size:       raw.Size,
			Created:    raw.Created,
			Containers: raw.Containers,
		})
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].Created > images[j].Created
	})

	return images, nil
}

func (s *DockerService) RemoveImage(imageID string, force bool) error {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return errors.New("operation unsupported by Dockiva Engine")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := s.client.Load().ImageRemove(
		ctx,
		imageID,
		client.ImageRemoveOptions{
			Force:         force,
			PruneChildren: true,
		},
	)
	return err
}

func (s *DockerService) ListVolumes() ([]VolumeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if runtime, ok := s.currentRuntime().(runtimeVolumeLister); ok {
		return runtime.ListRuntimeVolumes(ctx)
	}

	result, err := s.client.Load().VolumeList(
		ctx,
		client.VolumeListOptions{},
	)
	if err != nil {
		return nil, err
	}

	volumes := make([]VolumeInfo, 0, len(result.Items))

	for _, item := range result.Items {
		rawBytes, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}

		var raw rawVolume
		if err := json.Unmarshal(rawBytes, &raw); err != nil {
			return nil, err
		}

		if raw.Labels == nil {
			raw.Labels = map[string]string{}
		}

		volumes = append(volumes, VolumeInfo{
			Name:       raw.Name,
			Driver:     raw.Driver,
			Scope:      raw.Scope,
			Mountpoint: raw.Mountpoint,
			CreatedAt:  raw.CreatedAt,
			Labels:     raw.Labels,
		})
	}

	sort.Slice(volumes, func(i, j int) bool {
		return volumes[i].Name < volumes[j].Name
	})

	return volumes, nil
}

func (s *DockerService) RemoveVolume(name string, force bool) error {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return errors.New("operation unsupported by Dockiva Engine")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if runtime, ok := s.currentRuntime().(runtimeVolumeRemover); ok {
		_ = force
		return runtime.RemoveRuntimeVolume(ctx, name)
	}

	_, err := s.client.Load().VolumeRemove(
		ctx,
		name,
		client.VolumeRemoveOptions{
			Force: force,
		},
	)
	return err
}

func (s *DockerService) ListNetworks() ([]NetworkInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if runtime, ok := s.currentRuntime().(runtimeNetworkLister); ok {
		return runtime.ListRuntimeNetworks(ctx)
	}

	result, err := s.client.Load().NetworkList(
		ctx,
		client.NetworkListOptions{},
	)
	if err != nil {
		return nil, err
	}

	networks := make([]NetworkInfo, 0, len(result.Items))

	for _, item := range result.Items {
		rawBytes, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}

		var raw rawNetwork
		if err := json.Unmarshal(rawBytes, &raw); err != nil {
			return nil, err
		}

		networks = append(networks, NetworkInfo{
			ID:         raw.ID,
			ShortID:    shortenID(raw.ID),
			Name:       raw.Name,
			Driver:     raw.Driver,
			Scope:      raw.Scope,
			Internal:   raw.Internal,
			Attachable: raw.Attachable,
			Ingress:    raw.Ingress,
		})
	}

	sort.Slice(networks, func(i, j int) bool {
		return networks[i].Name < networks[j].Name
	})

	return networks, nil
}

func (s *DockerService) RemoveNetwork(networkID string) error {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return errors.New("operation unsupported by Dockiva Engine")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := s.client.Load().NetworkRemove(
		ctx,
		networkID,
		client.NetworkRemoveOptions{},
	)
	return err
}

func localhostURL(publicPort, privatePort uint16, protocol string) string {
	if publicPort == 0 || protocol != "tcp" || !likelyHTTPPort(privatePort) {
		return ""
	}

	scheme := "http"
	if privatePort == 443 || privatePort == 8443 {
		scheme = "https"
	}

	return fmt.Sprintf("%s://localhost:%d", scheme, publicPort)
}

func likelyHTTPPort(port uint16) bool {
	switch port {
	case 80, 443, 3000, 3001, 4000, 4173, 4200, 5000, 5173, 5174,
		7000, 8000, 8001, 8080, 8081, 8443, 8888, 9000:
		return true
	default:
		return false
	}
}

func shortenID(id string) string {
	prefix := ""
	value := id

	if strings.HasPrefix(value, "sha256:") {
		prefix = "sha256:"
		value = strings.TrimPrefix(value, "sha256:")
	}

	if len(value) > 12 {
		value = value[:12]
	}

	return prefix + value
}

func (s *DockerService) StartContainer(containerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return s.currentRuntime().StartContainer(ctx, containerID)
}

func (s *DockerService) StopContainer(containerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	return s.currentRuntime().StopContainer(ctx, containerID, 10*time.Second)
}

func (s *DockerService) RestartContainer(containerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	return s.currentRuntime().RestartContainer(ctx, containerID, 10*time.Second)
}

func (s *DockerService) RemoveContainer(containerID string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return s.currentRuntime().RemoveContainer(ctx, containerID, force)
}

func (s *DockerService) ComposeProjectAction(project string, action string) error {
	project = strings.TrimSpace(project)
	action = strings.ToLower(strings.TrimSpace(action))

	if project == "" {
		return fmt.Errorf("compose project name is required")
	}

	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("unsupported compose project action: %s", action)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cli, done, err := s.composeClient()
	if err != nil {
		return err
	}
	defer done()
	result, err := cli.ContainerList(
		ctx,
		client.ContainerListOptions{
			All: true,
		},
	)
	if err != nil {
		return err
	}

	type projectContainer struct {
		id   string
		name string
	}

	var targets []projectContainer

	for _, container := range result.Items {
		if container.Labels[composeProjectLabel] != project {
			continue
		}

		state := string(container.State)

		if action == "start" && state == "running" {
			continue
		}

		if (action == "stop" || action == "restart") && state != "running" {
			continue
		}

		name := container.ID
		if len(container.Names) > 0 {
			name = strings.TrimPrefix(container.Names[0], "/")
		}

		targets = append(targets, projectContainer{
			id:   container.ID,
			name: name,
		})
	}

	if len(targets) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(targets))

	for _, target := range targets {
		target := target

		wg.Add(1)
		go func() {
			defer wg.Done()

			var actionErr error
			timeout := 10

			switch action {
			case "start":
				_, actionErr = cli.ContainerStart(
					ctx,
					target.id,
					client.ContainerStartOptions{},
				)

			case "stop":
				_, actionErr = cli.ContainerStop(
					ctx,
					target.id,
					client.ContainerStopOptions{
						Timeout: &timeout,
					},
				)

			case "restart":
				_, actionErr = cli.ContainerRestart(
					ctx,
					target.id,
					client.ContainerRestartOptions{
						Timeout: &timeout,
					},
				)
			}

			if actionErr != nil {
				errCh <- fmt.Errorf("%s: %w", target.name, actionErr)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	var errors []string
	for actionErr := range errCh {
		errors = append(errors, actionErr.Error())
	}

	if len(errors) > 0 {
		sort.Strings(errors)
		return fmt.Errorf(
			"%s project %q completed with errors: %s",
			action,
			project,
			strings.Join(errors, "; "),
		)
	}

	return nil
}

func (s *DockerService) GetContainerStats(containerIDs []string) []ContainerResourceStats {
	if s.activeRuntimeProvider() != "docker" {
		if provider, ok := s.currentRuntime().(runtimeStatsProvider); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			return provider.RuntimeStats(ctx, containerIDs)
		}
	}

	if len(containerIDs) == 0 {
		return []ContainerResourceStats{}
	}

	if len(containerIDs) > 100 {
		containerIDs = containerIDs[:100]
	}

	results := make([]ContainerResourceStats, len(containerIDs))

	var wg sync.WaitGroup

	for i, containerID := range containerIDs {
		i := i
		containerID := containerID

		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i] = s.getSingleContainerStats(containerID)
		}()
	}

	wg.Wait()

	return results
}

func (s *DockerService) getSingleContainerStats(containerID string) ContainerResourceStats {
	result := ContainerResourceStats{
		ID: containerID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	statsResult, err := s.client.Load().ContainerStats(
		ctx,
		containerID,
		client.ContainerStatsOptions{
			Stream:                false,
			IncludePreviousSample: true,
		},
	)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer statsResult.Body.Close()

	var stats dockerStatsPayload

	if err := json.NewDecoder(statsResult.Body).Decode(&stats); err != nil {
		result.Error = err.Error()
		return result
	}

	cpuDelta := stats.CPUStats.CPUUsage.TotalUsage -
		stats.PreCPUStats.CPUUsage.TotalUsage

	systemDelta := stats.CPUStats.SystemUsage -
		stats.PreCPUStats.SystemUsage

	onlineCPUs := stats.CPUStats.OnlineCPUs
	if onlineCPUs == 0 {
		onlineCPUs = uint32(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}

	if systemDelta > 0 && cpuDelta > 0 {
		result.CPUPercent =
			(float64(cpuDelta) / float64(systemDelta)) *
				float64(onlineCPUs) *
				100
	}

	memoryUsage := stats.MemoryStats.Usage

	if inactive, ok := stats.MemoryStats.Stats["inactive_file"]; ok &&
		memoryUsage >= inactive {
		memoryUsage -= inactive
	} else if inactive, ok := stats.MemoryStats.Stats["total_inactive_file"]; ok &&
		memoryUsage >= inactive {
		memoryUsage -= inactive
	}

	result.MemoryUsage = memoryUsage
	result.MemoryLimit = stats.MemoryStats.Limit
	result.PIDs = stats.PidsStats.Current

	if result.MemoryLimit > 0 {
		result.MemoryPercent =
			(float64(result.MemoryUsage) / float64(result.MemoryLimit)) * 100
	}

	return result
}

// StartTerminal creates a persistent /bin/sh exec session with a TTY.
// The frontend receives output through the "dockiva:terminal-output" event.
func (s *DockerService) StartTerminal(containerID string) (string, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return "", fmt.Errorf("container ID is required")
	}

	if s.activeRuntimeProvider() != "docker" {
		return s.startRuntimeTerminal(containerID)
	}

	sessionID, err := newSessionID()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	execResult, err := s.client.Load().ExecCreate(
		ctx,
		containerID,
		client.ExecCreateOptions{
			TTY:          true,
			AttachStdin:  true,
			AttachStdout: true,
			AttachStderr: true,
			Env:          []string{"TERM=xterm-256color"},
			Cmd:          []string{"/bin/sh"},
			ConsoleSize: client.ConsoleSize{
				Height: 28,
				Width:  120,
			},
		},
	)
	if err != nil {
		return "", err
	}

	attachResult, err := s.client.Load().ExecAttach(
		context.Background(),
		execResult.ID,
		client.ExecAttachOptions{
			TTY: true,
			ConsoleSize: client.ConsoleSize{
				Height: 28,
				Width:  120,
			},
		},
	)
	if err != nil {
		return "", err
	}

	session := &terminalSession{
		id:          sessionID,
		containerID: containerID,
		execID:      execResult.ID,
		response:    attachResult,
	}

	s.sessionMu.Lock()
	s.terminalSessions[sessionID] = session
	s.sessionMu.Unlock()

	go s.readTerminal(session)

	return sessionID, nil
}

func (s *DockerService) SendTerminalInput(sessionID string, input string) error {
	s.sessionMu.Lock()
	session := s.terminalSessions[sessionID]
	s.sessionMu.Unlock()

	if session == nil || session.closed.Load() {
		return fmt.Errorf("terminal session is closed")
	}

	if binding, ok := getRuntimeTerminalBinding(sessionID); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return binding.provider.RuntimeTerminalInput(ctx, binding.runtimeID, input)
	}

	_, err := session.response.Conn.Write([]byte(input))
	return err
}

func (s *DockerService) ResizeTerminal(sessionID string, width uint, height uint) error {
	s.sessionMu.Lock()
	session := s.terminalSessions[sessionID]
	s.sessionMu.Unlock()

	if session == nil || session.closed.Load() {
		return fmt.Errorf("terminal session is closed")
	}

	if binding, ok := getRuntimeTerminalBinding(sessionID); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return binding.provider.RuntimeTerminalResize(ctx, binding.runtimeID, width, height)
	}

	if width == 0 {
		width = 120
	}
	if height == 0 {
		height = 28
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.client.Load().ExecResize(
		ctx,
		session.execID,
		client.ExecResizeOptions{
			Height: height,
			Width:  width,
		},
	)
	return err
}

func (s *DockerService) CloseTerminal(sessionID string) {
	s.sessionMu.Lock()
	session := s.terminalSessions[sessionID]
	delete(s.terminalSessions, sessionID)
	s.sessionMu.Unlock()

	if session == nil {
		return
	}

	if session.closed.CompareAndSwap(false, true) {
		if binding, ok := deleteRuntimeTerminalBinding(sessionID); ok {
			if binding.cancel != nil {
				binding.cancel()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = binding.provider.RuntimeTerminalClose(ctx, binding.runtimeID)
			cancel()
		} else {
			session.response.Close()
		}
	}

	s.emitStreamEvent(
		terminalOutputEvent,
		StreamOutputEvent{
			StreamID:    session.id,
			ContainerID: session.containerID,
			Closed:      true,
			Seq:         session.seq.Add(1),
		},
	)
}

func (s *DockerService) readTerminal(session *terminalSession) {
	buffer := make([]byte, 32*1024)

	for {
		n, err := session.response.Reader.Read(buffer)

		if n > 0 {
			s.emitStreamEvent(
				terminalOutputEvent,
				StreamOutputEvent{
					StreamID:    session.id,
					ContainerID: session.containerID,
					Data:        string(buffer[:n]),
					Seq:         session.seq.Add(1),
				},
			)
		}

		if err != nil {
			if err != io.EOF && !session.closed.Load() {
				s.emitStreamEvent(
					terminalOutputEvent,
					StreamOutputEvent{
						StreamID:    session.id,
						ContainerID: session.containerID,
						Error:       err.Error(),
						Seq:         session.seq.Add(1),
					},
				)
			}

			s.sessionMu.Lock()
			if s.terminalSessions[session.id] == session {
				delete(s.terminalSessions, session.id)
			}
			s.sessionMu.Unlock()

			if session.closed.CompareAndSwap(false, true) {
				session.response.Close()
			}

			s.emitStreamEvent(
				terminalOutputEvent,
				StreamOutputEvent{
					StreamID:    session.id,
					ContainerID: session.containerID,
					Closed:      true,
					Seq:         session.seq.Add(1),
				},
			)

			return
		}
	}
}

// StartLogStream follows Docker logs until StopLogStream is called or the
// container log stream ends. Output is emitted as "dockiva:log-output".
func (s *DockerService) StartLogStream(containerID string, tail int) (string, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return "", fmt.Errorf("container ID is required")
	}

	if tail <= 0 {
		tail = 200
	}
	if tail > 5000 {
		tail = 5000
	}

	if s.activeRuntimeProvider() != "docker" {
		return s.startRuntimeLogStream(containerID, tail)
	}

	streamID, err := newSessionID()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithCancel(context.Background())

	reader, err := s.client.Load().ContainerLogs(
		ctx,
		containerID,
		client.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Timestamps: true,
			Follow:     true,
			Tail:       strconv.Itoa(tail),
		},
	)
	if err != nil {
		cancel()
		return "", err
	}

	tty := s.containerUsesTTY(containerID)

	session := &logStreamSession{
		id:          streamID,
		containerID: containerID,
		cancel:      cancel,
	}

	s.sessionMu.Lock()
	s.logStreams[streamID] = session
	s.sessionMu.Unlock()

	go s.readLogStream(session, reader, tty)

	return streamID, nil
}

func (s *DockerService) StopLogStream(streamID string) {
	s.sessionMu.Lock()
	session := s.logStreams[streamID]
	delete(s.logStreams, streamID)
	s.sessionMu.Unlock()

	if session == nil {
		return
	}

	if session.closed.CompareAndSwap(false, true) {
		session.cancel()
	}

	s.emitStreamEvent(
		logOutputEvent,
		StreamOutputEvent{
			StreamID:    session.id,
			ContainerID: session.containerID,
			Closed:      true,
			Seq:         session.seq.Add(1),
		},
	)
}

func (s *DockerService) readLogStream(
	session *logStreamSession,
	reader io.ReadCloser,
	tty bool,
) {
	defer reader.Close()

	emitData := func(data []byte) {
		if len(data) == 0 || session.closed.Load() {
			return
		}

		s.emitStreamEvent(
			logOutputEvent,
			StreamOutputEvent{
				StreamID:    session.id,
				ContainerID: session.containerID,
				Data:        string(data),
				Seq:         session.seq.Add(1),
			},
		)
	}

	err := streamDockerOutput(reader, tty, emitData)

	s.sessionMu.Lock()
	if s.logStreams[session.id] == session {
		delete(s.logStreams, session.id)
	}
	s.sessionMu.Unlock()

	wasOpen := session.closed.CompareAndSwap(false, true)
	session.cancel()

	if err != nil && wasOpen && err != context.Canceled {
		s.emitStreamEvent(
			logOutputEvent,
			StreamOutputEvent{
				StreamID:    session.id,
				ContainerID: session.containerID,
				Error:       err.Error(),
				Seq:         session.seq.Add(1),
			},
		)
	}

	if wasOpen {
		s.emitStreamEvent(
			logOutputEvent,
			StreamOutputEvent{
				StreamID:    session.id,
				ContainerID: session.containerID,
				Closed:      true,
				Seq:         session.seq.Add(1),
			},
		)
	}
}

func streamDockerOutput(
	reader io.Reader,
	tty bool,
	emit func([]byte),
) error {
	if tty {
		buffer := make([]byte, 32*1024)

		for {
			n, err := reader.Read(buffer)

			if n > 0 {
				chunk := append([]byte(nil), buffer[:n]...)
				emit(chunk)
			}

			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
	}

	header := make([]byte, 8)

	for {
		_, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}

		size := int(binary.BigEndian.Uint32(header[4:8]))
		if size < 0 || size > 64*1024*1024 {
			return fmt.Errorf("invalid Docker stream frame size: %d", size)
		}

		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}

		emit(payload)
	}
}

func (s *DockerService) containerUsesTTY(containerID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := s.client.Load().ContainerInspect(
		ctx,
		containerID,
		client.ContainerInspectOptions{},
	)
	if err != nil || len(result.Raw) == 0 {
		return false
	}

	var inspect struct {
		Config struct {
			TTY bool `json:"Tty"`
		} `json:"Config"`
	}

	if err := json.Unmarshal(result.Raw, &inspect); err != nil {
		return false
	}

	return inspect.Config.TTY
}

func (s *DockerService) emitStreamEvent(name string, event StreamOutputEvent) {
	emitEvent(name, event)
}

func newSessionID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}

func (s *DockerService) ServiceShutdown() error {
	s.sessionMu.Lock()

	terminals := make([]*terminalSession, 0, len(s.terminalSessions))
	for _, session := range s.terminalSessions {
		terminals = append(terminals, session)
	}

	logs := make([]*logStreamSession, 0, len(s.logStreams))
	for _, session := range s.logStreams {
		logs = append(logs, session)
	}

	s.terminalSessions = make(map[string]*terminalSession)
	s.logStreams = make(map[string]*logStreamSession)

	s.sessionMu.Unlock()

	for _, session := range terminals {
		if session.closed.CompareAndSwap(false, true) {
			session.response.Close()
		}
	}

	for _, session := range logs {
		if session.closed.CompareAndSwap(false, true) {
			session.cancel()
		}
	}

	var firstErr error

	if runtimeCloser, ok := s.currentRuntime().(interface{ Close() error }); ok {
		if err := runtimeCloser.Close(); err != nil {
			firstErr = err
		}
	}

	if current := s.client.Load(); current != nil {
		if err := current.Close(); err != nil {
			firstErr = err
		}
	}

	s.retiredClientMu.Lock()
	retired := append([]*client.Client(nil), s.retiredClients...)
	s.retiredClients = nil
	s.retiredClientMu.Unlock()

	for _, retiredClient := range retired {
		if retiredClient == nil {
			continue
		}
		if err := retiredClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
