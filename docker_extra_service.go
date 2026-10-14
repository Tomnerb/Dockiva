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
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

const (
	composeWorkingDirLabel  = "com.docker.compose.project.working_dir"
	composeConfigFilesLabel = "com.docker.compose.project.config_files"
	composeEnvironmentLabel = "com.docker.compose.project.environment_file"
)

type ContainerMountDetail struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

type ContainerNetworkDetail struct {
	Name      string `json:"name"`
	IPAddress string `json:"ipAddress"`
	Gateway   string `json:"gateway"`
	Mac       string `json:"mac"`
}

type ContainerDetails struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Image         string                   `json:"image"`
	Created       string                   `json:"created"`
	Path          string                   `json:"path"`
	Args          []string                 `json:"args"`
	Entrypoint    []string                 `json:"entrypoint"`
	Command       []string                 `json:"command"`
	WorkingDir    string                   `json:"workingDir"`
	User          string                   `json:"user"`
	Environment   []string                 `json:"environment"`
	Mounts        []ContainerMountDetail   `json:"mounts"`
	Networks      []ContainerNetworkDetail `json:"networks"`
	Labels        map[string]string        `json:"labels"`
	RestartPolicy string                   `json:"restartPolicy"`
	State         string                   `json:"state"`
	ExitCode      int                      `json:"exitCode"`
	StartedAt     string                   `json:"startedAt"`
	FinishedAt    string                   `json:"finishedAt"`
}

type ComposeProjectDetails struct {
	Project     string   `json:"project"`
	WorkingDir  string   `json:"workingDir"`
	ConfigFiles []string `json:"configFiles"`
	EnvFile     string   `json:"envFile,omitempty"`
}

type DockerDiskUsageItem struct {
	Type        string `json:"type"`
	TotalCount  int    `json:"totalCount"`
	Active      int    `json:"active"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

type DockerCLIResult struct {
	Output string `json:"output"`
}

// DockerMigrationPreview is read-only discovery for the first-run migration
// assistant. It intentionally does not alter the existing Docker engine.
type DockerMigrationPreview struct {
	Available                bool   `json:"available"`
	Reachable                bool   `json:"reachable"`
	VolumeMigrationSupported bool   `json:"volumeMigrationSupported"`
	Endpoint                 string `json:"endpoint,omitempty"`
	Containers               int    `json:"containers"`
	Running                  int    `json:"running"`
	Images                   int    `json:"images"`
	Volumes                  int    `json:"volumes"`
	Message                  string `json:"message,omitempty"`
}

// DockerMigrationStatus is deliberately small and pollable. Image archives can
// be large, so migration must not keep a frontend RPC open for its full run.
type DockerMigrationStatus struct {
	Kind           string `json:"kind,omitempty"`
	State          string `json:"state"`
	Message        string `json:"message"`
	ImportedImages int    `json:"importedImages,omitempty"`
	TotalImages    int    `json:"totalImages,omitempty"`
	CurrentImage   string `json:"currentImage,omitempty"`
	Error          string `json:"error,omitempty"`
}

const dockerMigrationStatusEvent = "dockiva:migration-status"

func (s *DockerService) GetDockerMigrationStatus() DockerMigrationStatus {
	s.migrationMu.Lock()
	defer s.migrationMu.Unlock()
	if s.migrationStatus.State == "running" && !s.migrationStarted.IsZero() && time.Since(s.migrationStarted) > 15*time.Minute {
		s.migrationStatus = DockerMigrationStatus{
			State:   "failed",
			Message: "Docker image migration did not finish in time.",
			Error:   "The migration job exceeded its 15 minute safety limit. No Docker resources were deleted; retry after checking the reported error.",
		}
		s.migrationStarted = time.Time{}
	}
	return s.migrationStatus
}

func (s *DockerService) emitMigrationStatus(status DockerMigrationStatus) {
	// Wails can dispatch this while handling the RPC that starts migration.
	// Never let a UI event hold the worker launch or image-transfer loop.
	go emitEvent(dockerMigrationStatusEvent, status)
}

// StartDockerImageMigration starts a single tracked migration and returns
// immediately. The UI polls GetDockerMigrationStatus so a long archive export
// cannot strand its controls in a busy state if the RPC bridge is interrupted.
func (s *DockerService) StartDockerImageMigration() DockerMigrationStatus {
	s.migrationMu.Lock()
	if s.migrationStatus.State == "running" {
		status := s.migrationStatus
		s.migrationMu.Unlock()
		return status
	}
	s.migrationStatus = DockerMigrationStatus{
		State:   "running",
		Message: "Exporting Docker images. This can take several minutes for large images.",
	}
	s.migrationStarted = time.Now()
	status := s.migrationStatus
	s.migrationMu.Unlock()
	s.emitMigrationStatus(status)

	go func() {
		result, err := s.MigrateDockerImages()
		status := DockerMigrationStatus{State: "failed", Message: "Docker image migration failed."}
		if err != nil {
			status.Error = err.Error()
		} else {
			status.State = "complete"
			status.Message = result.Output
			status.ImportedImages = countImportedImages(result.Output)
			status.TotalImages = status.ImportedImages
		}
		s.migrationMu.Lock()
		s.migrationStatus = status
		s.migrationStarted = time.Time{}
		s.migrationMu.Unlock()
		s.emitMigrationStatus(status)
	}()

	return s.GetDockerMigrationStatus()
}

func (s *DockerService) StartDockerContainerMigration() DockerMigrationStatus {
	s.migrationMu.Lock()
	if s.migrationStatus.State == "running" {
		status := s.migrationStatus
		s.migrationMu.Unlock()
		return status
	}
	s.migrationStatus = DockerMigrationStatus{
		Kind:    "container",
		State:   "running",
		Message: "Preparing Docker containers for migration into Dockiva Native.",
	}
	s.migrationStarted = time.Now()
	status := s.migrationStatus
	s.migrationMu.Unlock()
	s.emitMigrationStatus(status)

	go func() {
		result, err := s.MigrateDockerContainers()
		status := DockerMigrationStatus{Kind: "container", State: "failed", Message: "Docker container migration failed."}
		if err != nil {
			status.Error = err.Error()
		} else {
			status.State = "complete"
			status.Message = result.Output
			status.ImportedImages = s.countMigratedFromMessage(result.Output)
			if status.ImportedImages == 0 {
				status.ImportedImages = status.TotalImages
			}
			if status.TotalImages == 0 {
				status.TotalImages = status.ImportedImages
			}
		}
		s.migrationMu.Lock()
		s.migrationStatus = status
		s.migrationStarted = time.Time{}
		s.migrationMu.Unlock()
		s.emitMigrationStatus(status)
	}()

	return s.GetDockerMigrationStatus()
}

func (s *DockerService) setDockerMigrationProgress(status DockerMigrationStatus) {
	s.migrationMu.Lock()
	if s.migrationStatus.State == "running" {
		status.State = "running"
		s.migrationStatus = status
	}
	updated := s.migrationStatus
	s.migrationMu.Unlock()
	s.emitMigrationStatus(updated)
}

func (s *DockerService) countMigratedFromMessage(message string) int {
	const expectedPrefix = "Migrated "
	if !strings.HasPrefix(message, expectedPrefix) {
		return 0
	}
	rest := strings.TrimPrefix(message, expectedPrefix)
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0
	}
	count, _ := strconv.Atoi(fields[0])
	return count
}

func countImportedImages(message string) int {
	fields := strings.Fields(message)
	if len(fields) < 2 {
		return 0
	}
	count, _ := strconv.Atoi(fields[1])
	return count
}

func (s *DockerService) GetDockerMigrationPreview() DockerMigrationPreview {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerMigrationPreview{Message: "Migration into Dockiva Engine is not available yet."}
	}

	preview := DockerMigrationPreview{
		Endpoint:                 s.dockerMigrationSourceEndpoint(),
		VolumeMigrationSupported: runtime.GOOS == "linux",
	}
	if preview.Endpoint == "" {
		preview.Message = "No external Docker endpoint is configured. Select External Docker once or choose an endpoint below."
		return preview
	}

	allContainers, err := runDockerCLIAtEndpoint(8*time.Second, "", preview.Endpoint, "container", "ls", "-aq")
	if err != nil {
		preview.Message = fmt.Sprintf("Docker is offline at %s. Start it to scan resources for migration.", preview.Endpoint)
		return preview
	}
	preview.Reachable = true
	running, err := runDockerCLIAtEndpoint(8*time.Second, "", preview.Endpoint, "container", "ls", "-q")
	if err != nil {
		preview.Message = err.Error()
		return preview
	}
	images, err := runDockerCLIAtEndpoint(8*time.Second, "", preview.Endpoint, "image", "ls", "-q")
	if err != nil {
		preview.Message = err.Error()
		return preview
	}
	volumes, err := runDockerCLIAtEndpoint(8*time.Second, "", preview.Endpoint, "volume", "ls", "-q")
	if err != nil {
		preview.Message = err.Error()
		return preview
	}

	preview.Containers = countDockerList(allContainers)
	preview.Running = countDockerList(running)
	preview.Images = countDockerList(images)
	preview.Volumes = countDockerList(volumes)
	preview.Available = preview.Containers > 0 || preview.Images > 0 || preview.Volumes > 0
	if preview.Available {
		preview.Message = "Existing Docker resources were found. Migration is opt-in and leaves Docker unchanged until verification succeeds."
	} else {
		preview.Message = "Docker is connected, but it has no containers, images, or volumes to migrate."
	}
	return preview
}

func (s *DockerService) StartDockerMigrationSource() DockerMigrationPreview {
	endpoint := s.dockerMigrationSourceEndpoint()
	if endpoint == "" {
		return s.GetDockerMigrationPreview()
	}
	if reachableDockerEndpoint(endpoint) {
		return s.GetDockerMigrationPreview()
	}
	started, err := startExternalDockerOnRequest(endpoint)
	if err != nil {
		return DockerMigrationPreview{Endpoint: endpoint, Message: err.Error()}
	}
	if !started {
		return DockerMigrationPreview{
			Endpoint: endpoint,
			Message:  fmt.Sprintf("Start the external Docker engine at %s, then scan again.", endpoint),
		}
	}
	return s.GetDockerMigrationPreview()
}

func (s *DockerService) dockerMigrationSourceEndpoint() string {
	endpoint := resolveExternalDockerEndpoint(s.ConfiguredDockerEndpoint())
	if endpoint != "" {
		s.SetConfiguredDockerEndpoint(endpoint)
	}
	return endpoint
}

func (s *DockerService) dockerMigrationSourceClient() (*client.Client, error) {
	endpoint := s.dockerMigrationSourceEndpoint()
	if endpoint == "" {
		return nil, errors.New("no external Docker endpoint is configured")
	}
	cli, err := client.New(
		client.WithHost(endpoint),
		client.WithAPIVersionNegotiation(),
		client.WithUserAgent("dockiva-migration/0.1"),
	)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("external Docker is offline at %s: %w", endpoint, err)
	}
	return cli, nil
}

// MigrateDockerImages copies Docker images into the Dockiva Native containerd
// namespace one at a time. This avoids a single giant archive, gives the user
// meaningful progress, and never modifies the source Docker store.
func (s *DockerService) MigrateDockerImages() (DockerCLIResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerCLIResult{}, errors.New("operation unsupported by Dockiva Engine")
	}

	images, err := runDockerCLIAtEndpoint(30*time.Second, "", s.dockerMigrationSourceEndpoint(), "image", "ls", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return DockerCLIResult{}, err
	}
	refs := uniqueDockerImageRefs(validDockerImageRefs(strings.Fields(images)))
	if len(refs) == 0 {
		return DockerCLIResult{Output: "No Docker images to migrate."}, nil
	}
	s.setDockerMigrationProgress(DockerMigrationStatus{
		Message:     "Preparing Docker images for migration…",
		TotalImages: len(refs),
	})

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return DockerCLIResult{}, errors.New("docker CLI was not found in PATH")
	}

	for index, ref := range refs {
		label := shortMigrationImageRef(ref)
		s.setDockerMigrationProgress(DockerMigrationStatus{
			Message:        fmt.Sprintf("Exporting image %d of %d…", index+1, len(refs)),
			TotalImages:    len(refs),
			ImportedImages: index,
			CurrentImage:   label,
		})
		if err := s.migrateSingleDockerImage(context.Background(), dockerPath, ref, index+1, len(refs)); err != nil {
			return DockerCLIResult{}, err
		}
		s.setDockerMigrationProgress(DockerMigrationStatus{
			Message:        fmt.Sprintf("Imported image %d of %d", index+1, len(refs)),
			TotalImages:    len(refs),
			ImportedImages: index + 1,
			CurrentImage:   label,
		})
	}
	return DockerCLIResult{Output: fmt.Sprintf("Imported %d Docker image(s) into Dockiva Native.", len(refs))}, nil
}

func (s *DockerService) migrateSingleDockerImage(parentCtx context.Context, dockerPath, ref string, index, total int) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("docker image reference is empty")
	}

	archive, err := os.CreateTemp("", "dockiva-docker-images-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())

	archivePath := archive.Name()
	exportCtx, exportCancel := context.WithTimeout(parentCtx, 10*time.Minute)
	cmd := exec.CommandContext(exportCtx, dockerPath, "--host", s.dockerMigrationSourceEndpoint(), "image", "save", "-o", archivePath, ref)
	cmd.Env = os.Environ()
	output, runErr := cmd.CombinedOutput()
	timedOut := exportCtx.Err() == context.DeadlineExceeded
	exportCloseErr := archive.Close()
	exportCancel()
	if runErr != nil || exportCloseErr != nil {
		if timedOut {
			return fmt.Errorf("Docker export timed out for image %s", ref)
		}
		if exportCloseErr != nil {
			return fmt.Errorf("export Docker image %s: %s", ref, strings.TrimSpace(exportCloseErr.Error()))
		}
		return fmt.Errorf("export Docker image %s: %s", ref, strings.TrimSpace(string(output)))
	}

	importCtx, importCancel := context.WithTimeout(parentCtx, 10*time.Minute)
	if total > 0 {
		importedImages := index - 1
		if importedImages < 0 {
			importedImages = 0
		}
		s.setDockerMigrationProgress(DockerMigrationStatus{
			Message:        fmt.Sprintf("Importing image %d of %d…", index, total),
			TotalImages:    total,
			ImportedImages: importedImages,
			CurrentImage:   shortMigrationImageRef(ref),
		})
	}
	importErr := s.importDockerImagesToNative(importCtx, archivePath)
	importCancel()
	if importErr != nil {
		return fmt.Errorf("import Docker image %s into Dockiva Native: %w", ref, importErr)
	}

	return nil
}

func validDockerImageRefs(refs []string) []string {
	valid := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.HasPrefix(ref, "<none>:") {
			continue
		}
		valid = append(valid, ref)
	}
	return valid
}

// MigrateDockerVolumes copies local Docker named-volume data into Dockiva's
// managed volume store. It never stops containers or deletes Docker volumes.
func (s *DockerService) MigrateDockerVolumes() (DockerCLIResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerCLIResult{}, errors.New("operation unsupported by Dockiva Engine")
	}

	output, err := runDockerCLIAtEndpoint(30*time.Second, "", s.dockerMigrationSourceEndpoint(), "volume", "ls", "-q")
	if err != nil {
		return DockerCLIResult{}, err
	}
	volumes := strings.Fields(output)
	s.emitMigrationStatus(DockerMigrationStatus{Kind: "volume", State: "running", Message: "Preparing named volumes…", TotalImages: len(volumes)})
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return DockerCLIResult{}, errors.New("docker CLI was not found in PATH")
	}
	for index, volume := range volumes {
		s.emitMigrationStatus(DockerMigrationStatus{Kind: "volume", State: "running", Message: fmt.Sprintf("Exporting volume %d of %d…", index+1, len(volumes)), TotalImages: len(volumes), ImportedImages: index, CurrentImage: volume})
		archive, createErr := os.CreateTemp("", "dockiva-docker-volume-*.tar")
		if createErr != nil {
			return DockerCLIResult{}, createErr
		}
		archivePath := archive.Name()
		defer os.Remove(archivePath)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		cmd := exec.CommandContext(ctx, dockerPath, "--host", s.dockerMigrationSourceEndpoint(), "run", "--rm", "--network", "none", "-v", volume+":/source:ro", "alpine:latest", "tar", "-C", "/source", "-cf", "-", ".")
		cmd.Stdout = archive
		var stderr strings.Builder
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		closeErr := archive.Close()
		if runErr != nil || closeErr != nil {
			cancel()
			return DockerCLIResult{}, fmt.Errorf("export Docker volume %s: %s", volume, strings.TrimSpace(stderr.String()))
		}
		copyErr := networkHelperUploadDockerVolume(ctx, volume, archivePath)
		cancel()
		if copyErr != nil {
			return DockerCLIResult{}, fmt.Errorf("copy Docker volume %s: %w", volume, copyErr)
		}
		s.emitMigrationStatus(DockerMigrationStatus{Kind: "volume", State: "running", Message: fmt.Sprintf("Copied volume %d of %d", index+1, len(volumes)), TotalImages: len(volumes), ImportedImages: index + 1, CurrentImage: volume})
	}
	s.emitMigrationStatus(DockerMigrationStatus{Kind: "volume", State: "complete", Message: fmt.Sprintf("Copied %d Docker volume(s) into Dockiva Native.", len(volumes)), TotalImages: len(volumes), ImportedImages: len(volumes)})
	return DockerCLIResult{Output: fmt.Sprintf("Copied %d Docker volume(s) into Dockiva Native.", len(volumes))}, nil
}

// MigrateDockerContainers copies stopped Docker containers into Dockiva Native.
func (s *DockerService) MigrateDockerContainers() (DockerCLIResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerCLIResult{}, errors.New("operation unsupported by Dockiva Engine")
	}

	activeRuntime := s.currentRuntime()
	runtime, ok := activeRuntime.(runtimeContainerCreator)
	if !ok {
		return DockerCLIResult{}, errors.New("container migration is not available for the active runtime")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	sourceClient, err := s.dockerMigrationSourceClient()
	if err != nil {
		return DockerCLIResult{}, err
	}
	defer sourceClient.Close()

	listResult, err := sourceClient.ContainerList(
		ctx,
		client.ContainerListOptions{All: true},
	)
	if err != nil {
		return DockerCLIResult{}, err
	}

	type dockerContainerSummary struct {
		ID    string
		Name  string
		Names []string
		Image string
		State string
	}

	stopped := make([]dockerContainerSummary, 0, len(listResult.Items))
	items := listResult.Items
	for _, item := range items {
		itemState := strings.TrimSpace(strings.ToLower(string(item.State)))
		if itemState == "" {
			itemState = strings.TrimSpace(strings.ToLower(string(item.State)))
		}

		if itemState == "running" {
			continue
		}
		stopped = append(stopped, dockerContainerSummary{
			ID:    item.ID,
			Name:  firstContainerName(item.Names),
			Names: item.Names,
			Image: item.Image,
			State: itemState,
		})
	}

	s.setDockerMigrationProgress(DockerMigrationStatus{
		Kind:        "container",
		State:       "running",
		Message:     "Preparing stopped Docker containers.",
		TotalImages: len(stopped),
	})

	if len(stopped) == 0 {
		return DockerCLIResult{Output: "No stopped Docker containers to migrate."}, nil
	}

	// Migration is retry-safe. A partial migration must not turn a retry into
	// dozens of "already exists" failures just because earlier containers were
	// successfully created before a later one failed.
	existingContainers, err := activeRuntime.ListContainers(ctx)
	if err != nil {
		return DockerCLIResult{}, fmt.Errorf("list Dockiva containers before migration: %w", err)
	}
	existingByName := make(map[string]struct{}, len(existingContainers))
	for _, existing := range existingContainers {
		if name := strings.TrimSpace(existing.Name); name != "" {
			existingByName[name] = struct{}{}
		}
	}

	runningCount := 0
	created := 0
	alreadyMigrated := 0
	failed := make([]string, 0)
	failReasons := make(map[string]string, 0)
	importedImages := make(map[string]bool)
	imageImportAttempted := make(map[string]bool)
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return DockerCLIResult{}, errors.New("docker CLI was not found in PATH")
	}

	for index, item := range stopped {
		name := item.Name
		if name == "" {
			for _, candidate := range item.Names {
				if strings.TrimSpace(candidate) != "" {
					name = candidate
					break
				}
			}
		}
		name = strings.TrimPrefix(name, "/")
		if name == "" {
			name = item.ID[:12]
		}
		name = "docker-" + sanitizeContainerdID(name)
		if name == "docker-" {
			name = fmt.Sprintf("docker-%s", item.ID[:12])
		}

		s.setDockerMigrationProgress(DockerMigrationStatus{
			Kind:           "container",
			State:          "running",
			Message:        fmt.Sprintf("Migrating container %d of %d…", index+1, len(stopped)),
			TotalImages:    len(stopped),
			ImportedImages: index,
			CurrentImage:   name,
		})

		if _, exists := existingByName[name]; exists {
			alreadyMigrated++
			s.setDockerMigrationProgress(DockerMigrationStatus{
				Kind:           "container",
				State:          "running",
				Message:        fmt.Sprintf("Container %d of %d is already in Dockiva", index+1, len(stopped)),
				TotalImages:    len(stopped),
				ImportedImages: index + 1,
				CurrentImage:   name,
			})
			continue
		}

		inspect, inspectErr := sourceClient.ContainerInspect(
			ctx,
			item.ID,
			client.ContainerInspectOptions{},
		)
		if inspectErr != nil {
			failed = append(failed, name)
			failReasons[name] = fmt.Sprintf("inspect failed: %s", inspectErr)
			continue
		}

		var raw struct {
			Config struct {
				Image      string   `json:"Image"`
				Entrypoint []string `json:"Entrypoint"`
				Cmd        []string `json:"Cmd"`
			} `json:"Config"`

			NetworkSettings struct {
				Ports map[string][]dockerPortBinding `json:"Ports"`
			} `json:"NetworkSettings"`
		}

		if err := json.Unmarshal(inspect.Raw, &raw); err != nil {
			failed = append(failed, name)
			failReasons[name] = fmt.Sprintf("inspect parse failed: %s", err)
			continue
		}

		ports := convertDockerPortsFromInspect(raw.NetworkSettings.Ports)

		command := raw.Config.Cmd
		if len(raw.Config.Entrypoint) > 0 {
			command = append(raw.Config.Entrypoint, command...)
		}

		imageRef := strings.TrimSpace(item.Image)
		configuredImage := strings.TrimSpace(raw.Config.Image)
		// Docker's summary API returns the immutable image ID for untagged
		// images. That ID cannot be used as a containerd image name. Inspect
		// retains the image reference that Docker used to create the container,
		// so prefer it whenever the summary value is an image ID.
		if imageRef == "" || imageRef == "<none>:<none>" || isDockerImageID(imageRef) {
			imageRef = configuredImage
		}
		imageRef = strings.TrimSpace(imageRef)

		createErr := s.createContainerWithImageFallback(
			ctx,
			runtime,
			imageRef,
			RuntimeCreateContainerRequest{
				Name:         name,
				Image:        imageRef,
				Command:      command,
				AutoStart:    false,
				PortMappings: ports,
			},
			dockerPath,
			importedImages,
			imageImportAttempted,
		)

		if createErr != nil {
			failed = append(failed, name)
			failReasons[name] = createErr.Error()
			continue
		}

		created++
		s.setDockerMigrationProgress(DockerMigrationStatus{
			Kind:           "container",
			State:          "running",
			Message:        fmt.Sprintf("Migrated container %d of %d", index+1, len(stopped)),
			TotalImages:    len(stopped),
			ImportedImages: index + 1,
			CurrentImage:   name,
		})
	}

	for _, item := range items {
		itemState := strings.TrimSpace(strings.ToLower(string(item.State)))
		if itemState == "running" {
			runningCount++
		}
	}

	if runningCount > 0 {
		s.emitMigrationStatus(DockerMigrationStatus{
			Kind:         "container",
			State:        "running",
			Message:      fmt.Sprintf("Skipped %d running containers; stop them first.", runningCount),
			TotalImages:  len(stopped),
			CurrentImage: fmt.Sprintf("%d stopped migrated", created),
		})
	}

	if len(failed) > 0 {
		limit := 6
		previewCount := 0
		reasonParts := make([]string, 0, len(failed))
		for _, name := range failed {
			if previewCount >= limit {
				break
			}
			reasonParts = append(reasonParts, fmt.Sprintf("%s: %s", name, failReasons[name]))
			previewCount++
		}
		if len(failed) > limit {
			reasonParts = append(reasonParts, fmt.Sprintf("+ %d more...", len(failed)-limit))
		}
		return DockerCLIResult{}, fmt.Errorf(
			"container migration partially completed: created %d, failed %d (%s)",
			created,
			len(failed),
			strings.Join(reasonParts, ", "),
		)
	}

	if alreadyMigrated > 0 {
		return DockerCLIResult{Output: fmt.Sprintf("Migrated %d stopped Docker container(s) into Dockiva Native; %d already migrated.", created, alreadyMigrated)}, nil
	}
	return DockerCLIResult{Output: fmt.Sprintf("Migrated %d stopped Docker container(s) into Dockiva Native.", created)}, nil
}

func isDockerImageID(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	digest := strings.TrimPrefix(value, "sha256:")
	if len(digest) != 64 {
		return false
	}
	for _, char := range digest {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (s *DockerService) createContainerWithImageFallback(
	ctx context.Context,
	runtime runtimeContainerCreator,
	imageRef string,
	request RuntimeCreateContainerRequest,
	dockerPath string,
	importedImages map[string]bool,
	imageImportAttempted map[string]bool,
) error {
	request.Image = imageRef

	_, createErr := runtime.CreateRuntimeContainer(ctx, request)
	if createErr == nil {
		return nil
	}

	if imageRef == "" {
		return fmt.Errorf("no image reference available: %w", createErr)
	}

	if !isContainerdImageMissing(createErr) {
		return createErr
	}

	if importedImages[imageRef] {
		return createErr
	}

	if imageImportAttempted[imageRef] {
		return createErr
	}
	imageImportAttempted[imageRef] = true

	s.setDockerMigrationProgress(DockerMigrationStatus{
		Kind:         "container",
		State:        "running",
		Message:      fmt.Sprintf("Importing image for container migration: %s", imageRef),
		TotalImages:  0,
		CurrentImage: shortMigrationImageRef(imageRef),
	})

	if err := s.migrateSingleDockerImage(context.Background(), dockerPath, imageRef, 0, 0); err != nil {
		return fmt.Errorf("image migration failed for %s: %w", imageRef, err)
	}
	importedImages[imageRef] = true

	_, createErr = runtime.CreateRuntimeContainer(ctx, request)
	if createErr == nil {
		return nil
	}
	return createErr
}

func isContainerdImageMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "not present in the dockiva namespace")
}

type dockerPortBinding struct {
	HostIP   string `json:"HostIP"`
	HostPort string `json:"HostPort"`
}

func convertDockerPortsToRuntime(bindings map[string][]dockerPortBinding) []RuntimePortMapping {
	return convertDockerPortBindings(bindings)
}

func convertDockerPortBindings(
	bindings map[string][]dockerPortBinding,
) []RuntimePortMapping {
	if len(bindings) == 0 {
		return []RuntimePortMapping{}
	}

	mappings := make([]RuntimePortMapping, 0, len(bindings))
	for rawPort, list := range bindings {
		parts := strings.Split(rawPort, "/")
		if len(parts) != 2 {
			continue
		}

		containerPort, parseErr := strconv.Atoi(parts[0])
		if parseErr != nil {
			continue
		}

		protocol := strings.ToLower(strings.TrimSpace(parts[1]))
		if protocol == "" {
			protocol = "tcp"
		}

		for _, binding := range list {
			hostPort, parseErr := strconv.Atoi(binding.HostPort)
			if parseErr != nil || hostPort < 1 || hostPort > 65535 {
				continue
			}

			mappings = append(mappings, RuntimePortMapping{
				HostPort:      int32(hostPort),
				ContainerPort: int32(containerPort),
				Protocol:      protocol,
				HostIP:        binding.HostIP,
			})
		}
	}

	return mappings
}

func convertDockerPortsFromInspect(bindings map[string][]dockerPortBinding) []RuntimePortMapping {
	if len(bindings) == 0 {
		return []RuntimePortMapping{}
	}
	return convertDockerPortBindings(bindings)
}

func shortMigrationImageRef(ref string) string {
	if len(ref) <= 18 {
		return ref
	}
	return ref[:18]
}

func uniqueDockerImageRefs(refs []string) []string {
	seen := make(map[string]struct{}, len(refs))
	unique := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		unique = append(unique, ref)
	}
	return unique
}

func countDockerList(output string) int {
	if strings.TrimSpace(output) == "" {
		return 0
	}
	return len(strings.Fields(output))
}

func firstContainerName(names []string) string {
	for _, value := range names {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *DockerService) GetContainerDetails(containerID string) (ContainerDetails, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var rawBytes []byte
	if r, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		data, err := r.Inspect(ctx, containerID)
		if err != nil {
			return ContainerDetails{}, err
		}
		rawBytes = data
	} else {
		result, err := s.client.Load().ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
		if err != nil {
			return ContainerDetails{}, err
		}
		rawBytes = result.Raw
	}

	var raw struct {
		ID      string   `json:"Id"`
		Name    string   `json:"Name"`
		Image   string   `json:"Image"`
		Created string   `json:"Created"`
		Path    string   `json:"Path"`
		Args    []string `json:"Args"`

		Config struct {
			Env        []string          `json:"Env"`
			Entrypoint []string          `json:"Entrypoint"`
			Cmd        []string          `json:"Cmd"`
			WorkingDir string            `json:"WorkingDir"`
			User       string            `json:"User"`
			Labels     map[string]string `json:"Labels"`
		} `json:"Config"`

		HostConfig struct {
			RestartPolicy struct {
				Name string `json:"Name"`
			} `json:"RestartPolicy"`
		} `json:"HostConfig"`

		State struct {
			Status     string `json:"Status"`
			ExitCode   int    `json:"ExitCode"`
			StartedAt  string `json:"StartedAt"`
			FinishedAt string `json:"FinishedAt"`
		} `json:"State"`

		Mounts []struct {
			Type        string `json:"Type"`
			Name        string `json:"Name"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`

		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress  string `json:"IPAddress"`
				Gateway    string `json:"Gateway"`
				MacAddress string `json:"MacAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}

	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return ContainerDetails{}, err
	}

	details := ContainerDetails{
		ID:            raw.ID,
		Name:          strings.TrimPrefix(raw.Name, "/"),
		Image:         raw.Image,
		Created:       raw.Created,
		Path:          raw.Path,
		Args:          raw.Args,
		Entrypoint:    raw.Config.Entrypoint,
		Command:       raw.Config.Cmd,
		WorkingDir:    raw.Config.WorkingDir,
		User:          raw.Config.User,
		Environment:   raw.Config.Env,
		Labels:        raw.Config.Labels,
		RestartPolicy: raw.HostConfig.RestartPolicy.Name,
		State:         raw.State.Status,
		ExitCode:      raw.State.ExitCode,
		StartedAt:     raw.State.StartedAt,
		FinishedAt:    raw.State.FinishedAt,
	}

	if details.Labels == nil {
		details.Labels = map[string]string{}
	}

	for _, mount := range raw.Mounts {
		details.Mounts = append(details.Mounts, ContainerMountDetail{
			Type:        mount.Type,
			Name:        mount.Name,
			Source:      mount.Source,
			Destination: mount.Destination,
			RW:          mount.RW,
		})
	}

	for name, network := range raw.NetworkSettings.Networks {
		details.Networks = append(details.Networks, ContainerNetworkDetail{
			Name:      name,
			IPAddress: network.IPAddress,
			Gateway:   network.Gateway,
			Mac:       network.MacAddress,
		})
	}

	return details, nil
}

func (s *DockerService) GetComposeProjectDetails(project string) (ComposeProjectDetails, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return ComposeProjectDetails{}, errors.New("operation unsupported by Dockiva Engine")
	}

	project = strings.TrimSpace(project)
	if project == "" {
		return ComposeProjectDetails{}, errors.New("compose project name is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := s.client.Load().ContainerList(
		ctx,
		client.ContainerListOptions{
			All: true,
		},
	)
	if err != nil {
		return ComposeProjectDetails{}, err
	}

	for _, container := range result.Items {
		if container.Labels[composeProjectLabel] != project {
			continue
		}

		workingDir := strings.TrimSpace(container.Labels[composeWorkingDirLabel])
		configFilesRaw := strings.TrimSpace(container.Labels[composeConfigFilesLabel])
		envFile := strings.TrimSpace(container.Labels[composeEnvironmentLabel])

		var configFiles []string
		for _, value := range strings.Split(configFilesRaw, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}

			if !filepath.IsAbs(value) && workingDir != "" {
				value = filepath.Join(workingDir, value)
			}

			configFiles = append(configFiles, filepath.Clean(value))
		}

		return ComposeProjectDetails{
			Project:     project,
			WorkingDir:  workingDir,
			ConfigFiles: configFiles,
			EnvFile:     envFile,
		}, nil
	}

	return ComposeProjectDetails{}, fmt.Errorf("compose project %q was not found", project)
}

func (s *DockerService) RunComposeCommand(project string, action string) (DockerCLIResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerCLIResult{}, errors.New("operation unsupported by Dockiva Engine")
	}

	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "up", "down", "build", "rebuild":
	default:
		return DockerCLIResult{}, fmt.Errorf("unsupported compose action: %s", action)
	}

	meta, err := s.GetComposeProjectDetails(project)
	if err != nil {
		return DockerCLIResult{}, err
	}

	if meta.WorkingDir == "" && len(meta.ConfigFiles) == 0 {
		return DockerCLIResult{}, fmt.Errorf(
			"compose project %q does not expose its working directory/config files",
			project,
		)
	}

	args := []string{
		"compose",
		"--ansi", "never",
		"-p", meta.Project,
	}

	if meta.WorkingDir != "" {
		args = append(args, "--project-directory", meta.WorkingDir)
	}

	if meta.EnvFile != "" {
		envFile := meta.EnvFile
		if !filepath.IsAbs(envFile) && meta.WorkingDir != "" {
			envFile = filepath.Join(meta.WorkingDir, envFile)
		}
		args = append(args, "--env-file", envFile)
	}

	for _, configFile := range meta.ConfigFiles {
		args = append(args, "-f", configFile)
	}

	switch action {
	case "up":
		args = append(args, "up", "-d")

	case "down":
		args = append(args, "down")

	case "build":
		args = append(args, "--progress", "plain", "build")

	case "rebuild":
		buildArgs := append([]string{}, args...)
		buildArgs = append(buildArgs, "--progress", "plain", "build")

		buildResult, err := runDockerCLI(30*time.Minute, meta.WorkingDir, buildArgs...)
		if err != nil {
			return DockerCLIResult{Output: buildResult}, err
		}

		upArgs := append([]string{}, args...)
		upArgs = append(upArgs, "up", "-d")

		upResult, err := runDockerCLI(10*time.Minute, meta.WorkingDir, upArgs...)
		output := strings.TrimSpace(buildResult + "\n" + upResult)

		return DockerCLIResult{Output: output}, err
	}

	output, err := runDockerCLI(30*time.Minute, meta.WorkingDir, args...)
	return DockerCLIResult{Output: output}, err
}

func (s *DockerService) PullImage(reference string) (DockerCLIResult, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return DockerCLIResult{}, errors.New("image reference is required")
	}

	// Dockiva Engine pulls through its own engine; never fall through to the Docker CLI.
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		result, err := s.RuntimePullImage(reference)
		return DockerCLIResult{Output: result.Detail}, err
	}

	output, err := runDockerCLI(30*time.Minute, "", "pull", reference)
	return DockerCLIResult{Output: output}, err
}

func (s *DockerService) CreateVolume(name string, driver string) (DockerCLIResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerCLIResult{}, errors.New("operation unsupported by Dockiva Engine")
	}

	name = strings.TrimSpace(name)
	driver = strings.TrimSpace(driver)

	if name == "" {
		return DockerCLIResult{}, errors.New("volume name is required")
	}

	args := []string{"volume", "create"}

	if driver != "" && driver != "local" {
		args = append(args, "--driver", driver)
	}

	args = append(args, name)

	output, err := runDockerCLI(2*time.Minute, "", args...)
	return DockerCLIResult{Output: output}, err
}

func (s *DockerService) CreateNetwork(name string, driver string) (DockerCLIResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerCLIResult{}, errors.New("operation unsupported by Dockiva Engine")
	}

	name = strings.TrimSpace(name)
	driver = strings.TrimSpace(driver)

	if name == "" {
		return DockerCLIResult{}, errors.New("network name is required")
	}

	if driver == "" {
		driver = "bridge"
	}

	switch driver {
	case "bridge", "overlay", "macvlan", "ipvlan":
	default:
		return DockerCLIResult{}, fmt.Errorf("unsupported network driver: %s", driver)
	}

	args := []string{
		"network", "create",
		"--driver", driver,
		name,
	}

	output, err := runDockerCLI(2*time.Minute, "", args...)
	return DockerCLIResult{Output: output}, err
}

func (s *DockerService) GetDiskUsage() ([]DockerDiskUsageItem, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return nil, errors.New("resource accounting unavailable for Dockiva Engine")
	}

	output, err := runDockerCLI(
		2*time.Minute,
		"",
		"system", "df",
		"--format", "json",
	)
	if err != nil {
		return nil, err
	}

	var items []DockerDiskUsageItem

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw struct {
			Type        string `json:"Type"`
			TotalCount  string `json:"TotalCount"`
			Active      string `json:"Active"`
			Size        string `json:"Size"`
			Reclaimable string `json:"Reclaimable"`
		}

		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("parse docker disk usage: %w", err)
		}

		totalCount, _ := strconv.Atoi(raw.TotalCount)
		active, _ := strconv.Atoi(raw.Active)

		items = append(items, DockerDiskUsageItem{
			Type:        raw.Type,
			TotalCount:  totalCount,
			Active:      active,
			Size:        raw.Size,
			Reclaimable: raw.Reclaimable,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (s *DockerService) PruneDocker(scope string) (DockerCLIResult, error) {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return DockerCLIResult{}, errors.New("operation unsupported by Dockiva Engine")
	}

	scope = strings.ToLower(strings.TrimSpace(scope))

	var args []string

	switch scope {
	case "containers":
		args = []string{"container", "prune", "-f"}

	case "images":
		// Intentionally prune dangling images only. We do not use -a.
		args = []string{"image", "prune", "-f"}

	case "networks":
		args = []string{"network", "prune", "-f"}

	case "volumes":
		// Docker volume prune defaults to anonymous unused volumes.
		// Named volumes are intentionally not included.
		args = []string{"volume", "prune", "-f"}

	case "system":
		// Intentionally excludes volumes and unused tagged images.
		args = []string{"system", "prune", "-f"}

	default:
		return DockerCLIResult{}, fmt.Errorf("unsupported prune scope: %s", scope)
	}

	output, err := runDockerCLI(10*time.Minute, "", args...)
	return DockerCLIResult{Output: output}, err
}

func runDockerCLI(timeout time.Duration, dir string, args ...string) (string, error) {
	return runDockerCLIAtEndpoint(timeout, dir, "", args...)
}

func runDockerCLIAtEndpoint(timeout time.Duration, dir string, endpoint string, args ...string) (string, error) {
	dockerPath, err := dockerCLIPath()
	if err != nil {
		return "", errors.New("docker CLI was not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	commandArgs := args
	if strings.TrimSpace(endpoint) != "" {
		commandArgs = append([]string{"--host", endpoint}, args...)
	}
	cmd := exec.CommandContext(ctx, dockerPath, commandArgs...)

	if dir != "" {
		if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
			cmd.Dir = dir
		}
	}

	cmd.Env = append(
		os.Environ(),
		"COMPOSE_MENU=false",
	)

	outputBytes, runErr := cmd.CombinedOutput()
	output := strings.TrimSpace(string(outputBytes))

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("docker command timed out")
	}

	if runErr != nil {
		if output == "" {
			return output, runErr
		}

		return output, fmt.Errorf("%s", output)
	}

	return output, nil
}
