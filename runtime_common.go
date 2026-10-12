package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type RuntimeCapabilities struct {
	Containers      bool `json:"containers"`
	Lifecycle       bool `json:"lifecycle"`
	Stats           bool `json:"stats"`
	Logs            bool `json:"logs"`
	Terminal        bool `json:"terminal"`
	Images          bool `json:"images"`
	PullImages      bool `json:"pullImages"`
	CreateContainer bool `json:"createContainer"`
	Volumes         bool `json:"volumes"`
	Networks        bool `json:"networks"`
	PortPublishing  bool `json:"portPublishing"`
	DNS             bool `json:"dns"`
	Compose         bool `json:"compose"`
}

type ContainerRuntimeInfo struct {
	Provider     string              `json:"provider"`
	DisplayName  string              `json:"displayName"`
	Endpoint     string              `json:"endpoint,omitempty"`
	Connected    bool                `json:"connected"`
	Experimental bool                `json:"experimental"`
	Capabilities RuntimeCapabilities `json:"capabilities"`
	Message      string              `json:"message,omitempty"`
}

type RuntimeCandidateInfo struct {
	Provider     string `json:"provider"`
	DisplayName  string `json:"displayName"`
	Available    bool   `json:"available"`
	Detected     bool   `json:"detected"`
	Selectable   bool   `json:"selectable"`
	Experimental bool   `json:"experimental"`
	Endpoint     string `json:"endpoint,omitempty"`
	Message      string `json:"message,omitempty"`
}

type RuntimeOverview struct {
	Active     ContainerRuntimeInfo   `json:"active"`
	Candidates []RuntimeCandidateInfo `json:"candidates"`
}

type RuntimeOperationResult struct {
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type RuntimePortMapping struct {
	HostPort      int32  `json:"hostPort"`
	ContainerPort int32  `json:"containerPort"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"hostIp"`
}

type RuntimeCreateContainerRequest struct {
	Name         string               `json:"name"`
	Image        string               `json:"image"`
	Command      []string             `json:"command"`
	AutoStart    bool                 `json:"autoStart"`
	PortMappings []RuntimePortMapping `json:"portMappings"`
}

// ContainerRuntime is the runtime-neutral lifecycle seam.
//
// Docker-specific higher-level concepts such as Compose and Docker networking
// intentionally stay out of this interface.
type ContainerRuntime interface {
	Info(ctx context.Context) ContainerRuntimeInfo
	ListContainers(ctx context.Context) ([]ContainerInfo, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RestartContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, containerID string, force bool) error
}

type runtimeImagePuller interface {
	PullRuntimeImage(ctx context.Context, reference string) (RuntimeOperationResult, error)
}

type runtimeImageLister interface {
	ListRuntimeImages(ctx context.Context) ([]ImageInfo, error)
}

type runtimeNetworkLister interface {
	ListRuntimeNetworks(ctx context.Context) ([]NetworkInfo, error)
}

type runtimeVolumeLister interface {
	ListRuntimeVolumes(ctx context.Context) ([]VolumeInfo, error)
}

type runtimeVolumeRemover interface {
	RemoveRuntimeVolume(ctx context.Context, name string) error
}

type runtimeContainerCreator interface {
	CreateRuntimeContainer(
		ctx context.Context,
		request RuntimeCreateContainerRequest,
	) (ContainerInfo, error)
}

type offlineRuntime struct {
	info ContainerRuntimeInfo
}

func (r *offlineRuntime) Info(context.Context) ContainerRuntimeInfo {
	return r.info
}

func (r *offlineRuntime) unavailable() error {
	return fmt.Errorf("%s is offline: %s", r.info.DisplayName, r.info.Message)
}

func (r *offlineRuntime) ListContainers(context.Context) ([]ContainerInfo, error) {
	return nil, r.unavailable()
}

func (r *offlineRuntime) StartContainer(context.Context, string) error {
	return r.unavailable()
}

func (r *offlineRuntime) StopContainer(context.Context, string, time.Duration) error {
	return r.unavailable()
}

func (r *offlineRuntime) RestartContainer(context.Context, string, time.Duration) error {
	return r.unavailable()
}

func (r *offlineRuntime) RemoveContainer(context.Context, string, bool) error {
	return r.unavailable()
}

func (s *DockerService) replaceRuntime(next ContainerRuntime) {
	s.runtimeMu.Lock()
	previous := s.runtime
	s.runtime = next
	s.runtimeMu.Unlock()

	if previous != nil && previous != next {
		if closer, ok := previous.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
}

func (s *DockerService) useOfflineNativeRuntime(status EngineStatus) string {
	provider := nativeRuntimeProvider()
	displayName := "Dockiva Native"
	if status.Platform == "darwin" {
		displayName = "Dockiva Native VM"
	} else if status.Platform == "windows" {
		displayName = "Dockiva WSL2"
	}

	s.replaceRuntime(&offlineRuntime{info: ContainerRuntimeInfo{
		Provider:     provider,
		DisplayName:  displayName,
		Endpoint:     status.Endpoint,
		Connected:    false,
		Experimental: true,
		Message:      status.Message,
	}})
	return provider
}

func (s *DockerService) currentRuntime() ContainerRuntime {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()

	if s.runtime != nil {
		return s.runtime
	}

	return newDockerRuntime(s)
}

func (s *DockerService) GetRuntimeOverview() RuntimeOverview {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	active := s.currentRuntime().Info(ctx)

	return RuntimeOverview{
		Active:     active,
		Candidates: append(platformRuntimeCandidates(), vmfreeCandidate()),
	}
}

func (s *DockerService) SelectContainerRuntime(provider string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "docker"
	}

	var (
		next ContainerRuntime
		err  error
	)

	switch provider {
	case "vmfree":
		next = newVMFreeRuntime()
	case "docker", "moby":
		next = newDockerRuntime(s)
	default:
		next, err = createPlatformRuntime(s, provider)
		if err != nil {
			return err
		}
	}

	if next == nil {
		return fmt.Errorf("runtime %q could not be created", provider)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	info := next.Info(ctx)
	cancel()

	if !info.Connected && provider != "docker" && provider != "moby" {
		if closer, ok := next.(interface{ Close() error }); ok {
			_ = closer.Close()
		}

		return fmt.Errorf(
			"runtime %q is not connected: %s",
			provider,
			info.Message,
		)
	}

	s.replaceRuntime(next)

	return nil
}

func (s *DockerService) RuntimePullImage(
	reference string,
) (RuntimeOperationResult, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return RuntimeOperationResult{}, errors.New("image reference is required")
	}

	runtime := s.currentRuntime()

	puller, ok := runtime.(runtimeImagePuller)
	if !ok {
		return RuntimeOperationResult{},
			errors.New("the active runtime does not expose runtime-level image pull")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	return puller.PullRuntimeImage(ctx, reference)
}

func (s *DockerService) RuntimeCreateContainer(
	name string,
	image string,
	command []string,
	autoStart bool,
	portMappings []RuntimePortMapping,
) (ContainerInfo, error) {
	request := RuntimeCreateContainerRequest{
		Name:         strings.TrimSpace(name),
		Image:        strings.TrimSpace(image),
		Command:      command,
		AutoStart:    autoStart,
		PortMappings: portMappings,
	}

	if request.Name == "" {
		return ContainerInfo{}, errors.New("container name is required")
	}
	if request.Image == "" {
		return ContainerInfo{}, errors.New("image reference is required")
	}

	runtime := s.currentRuntime()

	creator, ok := runtime.(runtimeContainerCreator)
	if !ok {
		return ContainerInfo{},
			errors.New("the active runtime does not expose runtime-level container creation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	return creator.CreateRuntimeContainer(ctx, request)
}
