package main

// Migration from an external Docker (Docker Desktop, OrbStack…) into Dockiva Engine.
// The source is only read: images are saved, volumes are read by throwaway --rm
// helper containers, and containers are inspected. Nothing on it is stopped,
// changed or deleted.

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// dockerHost is the engine's plain Docker API socket (no token), for the docker CLI.
func (r *vmfreeRuntime) dockerHost() string { return "unix://" + filepath.Join(r.home, "docker.sock") }

func (s *DockerService) migrationTargetName() string {
	if _, ok := s.currentRuntime().(*vmfreeRuntime); ok {
		return "Dockiva Engine"
	}
	return "Dockiva Native"
}

func engineNotFound(err error) bool { return err != nil && strings.Contains(err.Error(), "HTTP 404") }

// splitArm64Refs keeps the images Dockiva Engine can run (linux/arm64). archOf reads
// an image's architecture on the source; an image it cannot read is skipped too.
func splitArm64Refs(refs []string, archOf func(string) (string, error)) (keep, skipped []string) {
	for _, ref := range refs {
		if arch, err := archOf(ref); err == nil && strings.TrimSpace(arch) == "arm64" {
			keep = append(keep, ref)
		} else {
			skipped = append(skipped, ref)
		}
	}
	return keep, skipped
}

func summarizeMigrationItems(items []string) string {
	const limit = 6
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:limit], ", ") + fmt.Sprintf(", + %d more", len(items)-limit)
}

// --- Volumes ---

// pickVolumeHelperImage returns an official busybox or alpine image the source
// already has, so reading volumes needs no pull.
func pickVolumeHelperImage(refs []string) string {
	for _, want := range []string{"busybox", "alpine"} {
		for _, ref := range refs {
			i := strings.LastIndex(ref, ":")
			if i < 0 || strings.Contains(ref[i:], "/") {
				continue
			}
			repo, tag := ref[:i], ref[i+1:]
			repo = strings.TrimPrefix(strings.TrimPrefix(repo, "docker.io/"), "library/")
			if repo == want && tag != "" && tag != "<none>" {
				return ref
			}
		}
	}
	return ""
}

func (s *DockerService) migrateDockerVolumesToEngine(r *vmfreeRuntime) (DockerCLIResult, error) {
	source := s.dockerMigrationSourceEndpoint()
	output, err := runDockerCLIAtEndpoint(30*time.Second, "", source, "volume", "ls", "-q")
	if err != nil {
		return DockerCLIResult{}, err
	}
	volumes := strings.Fields(output)
	if len(volumes) == 0 {
		return DockerCLIResult{Output: "No Docker volumes to migrate."}, nil
	}
	progress := func(message string, done int, current string) {
		s.emitMigrationStatus(DockerMigrationStatus{Kind: "volume", State: "running", Message: message, TotalImages: len(volumes), ImportedImages: done, CurrentImage: current})
	}
	progress("Preparing named volumes…", 0, "")

	images, err := runDockerCLIAtEndpoint(30*time.Second, "", source, "image", "ls", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return DockerCLIResult{}, err
	}
	helper, pulled := pickVolumeHelperImage(strings.Fields(images)), false
	if helper == "" {
		helper, pulled = "busybox:latest", true
		progress("Pulling busybox:latest on your Docker to read volumes…", 0, helper)
		if _, err := runDockerCLIAtEndpoint(5*time.Minute, "", source, "pull", helper); err != nil {
			return DockerCLIResult{}, fmt.Errorf("pull busybox:latest on your Docker to read volumes: %w", err)
		}
	}

	copied := 0
	var existing, failed []string
	for index, name := range volumes {
		progress(fmt.Sprintf("Copying volume %d of %d…", index+1, len(volumes)), index, name)
		done, err := s.copyVolumeToEngine(r, source, helper, name)
		switch {
		case err != nil:
			failed = append(failed, fmt.Sprintf("%s: %s", name, err))
		case done:
			copied++
		default:
			existing = append(existing, name)
		}
	}

	message := fmt.Sprintf("Copied %d Docker volume(s) into Dockiva Engine.", copied)
	if len(existing) > 0 {
		message += fmt.Sprintf(" Skipped %d that already have data in Dockiva Engine: %s.", len(existing), summarizeMigrationItems(existing))
	}
	if pulled {
		message += " busybox:latest was pulled on your Docker to read the volumes."
	}
	if len(failed) > 0 {
		return DockerCLIResult{}, fmt.Errorf("%s Failed %d: %s", message, len(failed), summarizeMigrationItems(failed))
	}
	s.emitMigrationStatus(DockerMigrationStatus{Kind: "volume", State: "complete", Message: message, TotalImages: len(volumes), ImportedImages: len(volumes)})
	return DockerCLIResult{Output: message}, nil
}

// copyVolumeToEngine copies one source volume into a Dockiva Engine volume. It
// returns false, and copies nothing, when the target volume already holds data.
func (s *DockerService) copyVolumeToEngine(r *vmfreeRuntime, source, helper, name string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	var labels map[string]string
	if out, err := runDockerCLIAtEndpoint(30*time.Second, "", source, "volume", "inspect", "--format", "{{json .Labels}}", name); err == nil {
		_ = json.Unmarshal([]byte(out), &labels)
	}
	dir, err := r.ensureVolume(ctx, name, labels)
	if err != nil {
		return false, err
	}
	if entries, err := os.ReadDir(dir); err != nil {
		return false, err
	} else if len(entries) > 0 {
		return false, nil
	}

	dockerPath, err := dockerCLIPath()
	if err != nil {
		return false, errors.New("docker CLI was not found")
	}
	cmd := exec.CommandContext(ctx, dockerPath, "--host", source, "run", "--rm", "--network", "none", "-v", name+":/source:ro", helper, "tar", "-C", "/source", "-cf", "-", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	extractErr := extractTarSafely(stdout, dir)
	// Drain so the helper's tar finishes and --rm removes it on the source.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if extractErr == nil && waitErr != nil {
		extractErr = fmt.Errorf("read volume on the source: %s", strings.TrimSpace(firstNonEmpty(stderr.String(), waitErr.Error())))
	}
	if extractErr != nil {
		// The directory was empty before, so clearing it only removes this partial copy.
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
			}
		}
		return false, extractErr
	}
	return true, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// ensureVolume creates the volume through the engine API when needed and returns
// its host directory, which must lie inside the engine's home.
func (r *vmfreeRuntime) ensureVolume(ctx context.Context, name string, labels map[string]string) (string, error) {
	data, err := r.request(ctx, "GET", "/volumes/"+url.PathEscape(name), nil)
	if engineNotFound(err) {
		data, err = r.request(ctx, "POST", "/volumes/create", map[string]any{"Name": name, "Labels": labels})
	}
	if err != nil {
		return "", err
	}
	var volume struct{ Mountpoint string }
	if err := json.Unmarshal(data, &volume); err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(volume.Mountpoint)
	if err != nil {
		return "", fmt.Errorf("volume directory: %w", err)
	}
	home, err := filepath.EvalSymlinks(r.home)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(dir, home+string(filepath.Separator)) {
		return "", fmt.Errorf("volume directory %s is outside Dockiva Engine's home", volume.Mountpoint)
	}
	return dir, nil
}

// extractTarSafely unpacks a tar stream into dir. Absolute names and ".." are
// refused, and no path is resolved through a symlink: symlinks are recreated as
// links, never followed. Ownership is not restored; devices and FIFOs are skipped.
func extractTarSafely(r io.Reader, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	type dirMeta struct {
		name  string
		mode  fs.FileMode
		mtime time.Time
	}
	var dirs []dirMeta
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name, err := safeTarPath(hdr.Name)
		if err != nil {
			return err
		}
		if err := makeTarParents(root, name); err != nil {
			return err
		}
		mode := fs.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if name != "." {
				if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
					return err
				}
				if info, err := root.Lstat(name); err != nil || !info.IsDir() {
					return fmt.Errorf("%s: not a directory", name)
				}
			}
			dirs = append(dirs, dirMeta{name, mode, hdr.ModTime})
		case tar.TypeReg:
			if err := removeTarEntry(root, name); err != nil {
				return err
			}
			file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			copyErr := copySparse(file, tr, hdr.Size)
			if closeErr := file.Close(); copyErr == nil {
				copyErr = closeErr
			}
			if copyErr != nil {
				return copyErr
			}
			if err := root.Chmod(name, mode); err != nil {
				return err
			}
			_ = root.Chtimes(name, hdr.AccessTime, hdr.ModTime)
		case tar.TypeSymlink:
			if err := removeTarEntry(root, name); err != nil {
				return err
			}
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return err
			}
		case tar.TypeLink:
			target, err := safeTarPath(hdr.Linkname)
			if err != nil {
				return err
			}
			if err := makeTarParents(root, target); err != nil {
				return err
			}
			if info, err := root.Lstat(target); err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("%s: hard link target %s is not a regular file", name, target)
			}
			if err := removeTarEntry(root, name); err != nil {
				return err
			}
			if err := root.Link(target, name); err != nil {
				return err
			}
		}
	}
	// Directory modes last, deepest first, so read-only directories can be filled.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := root.Chmod(dirs[i].name, dirs[i].mode); err != nil {
			return err
		}
		_ = root.Chtimes(dirs[i].name, time.Time{}, dirs[i].mtime)
	}
	return nil
}

// copySparse writes size bytes, seeking over zero blocks so database files with
// holes (MongoDB's preallocated journal, for one) stay small on disk.
func copySparse(file *os.File, r io.Reader, size int64) error {
	buf := make([]byte, 64*1024)
	var total int64
	for {
		n, err := io.ReadFull(r, buf)
		total += int64(n)
		if n > 0 {
			if bytes.Count(buf[:n], []byte{0}) == n {
				_, seekErr := file.Seek(int64(n), io.SeekCurrent)
				if seekErr != nil {
					return seekErr
				}
			} else if _, writeErr := file.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if (err == io.EOF || err == io.ErrUnexpectedEOF) && total == size {
			return file.Truncate(size)
		}
		if err != nil {
			return err
		}
	}
}

func safeTarPath(name string) (string, error) {
	if name == "" || path.IsAbs(name) {
		return "", fmt.Errorf("unsafe path in volume archive: %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe path in volume archive: %q", name)
		}
	}
	return path.Clean(name), nil
}

// makeTarParents creates name's missing parent directories and refuses a parent
// that is a symlink or a file, so nothing is written through a link.
func makeTarParents(root *os.Root, name string) error {
	parent := path.Dir(name)
	if parent == "." {
		return nil
	}
	parts := strings.Split(parent, "/")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Mkdir(prefix, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s: parent %s is not a directory", name, prefix)
		}
	}
	return nil
}

func removeTarEntry(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s: is a directory", name)
	}
	return root.Remove(name)
}

// --- Containers ---

// migrationInspect is the part of `docker inspect` a container migration keeps.
type migrationInspect struct {
	ID     string `json:"Id"`
	Name   string
	Config struct {
		Image                string
		Env, Cmd, Entrypoint []string
		WorkingDir, User     string
		ExposedPorts         map[string]struct{}
		Labels               map[string]string
		Healthcheck          json.RawMessage
	}
	HostConfig struct {
		NetworkMode   string
		PortBindings  map[string][]dockerPortBinding
		RestartPolicy struct {
			Name              string
			MaximumRetryCount int
		}
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	NetworkSettings struct {
		Networks map[string]struct{ Aliases []string }
	}
}

type engineMount struct {
	Type     string
	Source   string `json:",omitempty"`
	Target   string
	ReadOnly bool `json:",omitempty"`
}

type enginePortBinding struct{ HostIp, HostPort string }

type engineRestartPolicy struct {
	Name              string
	MaximumRetryCount int `json:",omitempty"`
}

type engineEndpoint struct {
	Aliases []string `json:",omitempty"`
}

type engineContainerCreate struct {
	Image        string
	Env          []string `json:",omitempty"`
	Cmd          []string // no omitempty: an explicit [] must not become the image default
	Entrypoint   []string
	WorkingDir   string              `json:",omitempty"`
	User         string              `json:",omitempty"`
	ExposedPorts map[string]struct{} `json:",omitempty"`
	Labels       map[string]string   `json:",omitempty"`
	Healthcheck  json.RawMessage     `json:",omitempty"`
	HostConfig   struct {
		NetworkMode   string                         `json:",omitempty"`
		PortBindings  map[string][]enginePortBinding `json:",omitempty"`
		Mounts        []engineMount                  `json:",omitempty"`
		RestartPolicy *engineRestartPolicy           `json:",omitempty"`
	}
	NetworkingConfig struct {
		EndpointsConfig map[string]engineEndpoint `json:",omitempty"`
	}
}

func builtinDockerNetwork(name string) bool {
	switch name {
	case "", "default", "bridge", "host", "none":
		return true
	}
	return false
}

// engineCreateFromInspect translates a source container's inspect into a Dockiva
// Engine create request. It returns the container name and the user networks
// that must exist on the engine first.
func engineCreateFromInspect(raw []byte) (string, engineContainerCreate, []string, error) {
	var in migrationInspect
	var out engineContainerCreate
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", out, nil, err
	}
	c := in.Config
	out.Image, out.Env, out.Cmd, out.Entrypoint = c.Image, c.Env, c.Cmd, c.Entrypoint
	out.WorkingDir, out.User, out.ExposedPorts, out.Labels = c.WorkingDir, c.User, c.ExposedPorts, c.Labels
	if string(c.Healthcheck) != "null" {
		out.Healthcheck = c.Healthcheck
	}

	if len(in.HostConfig.PortBindings) > 0 {
		out.HostConfig.PortBindings = map[string][]enginePortBinding{}
		for port, bindings := range in.HostConfig.PortBindings {
			for _, b := range bindings {
				// ponytail: Dockiva Engine publishes on 127.0.0.1 only, so 0.0.0.0 becomes loopback.
				out.HostConfig.PortBindings[port] = append(out.HostConfig.PortBindings[port], enginePortBinding{HostIp: "127.0.0.1", HostPort: b.HostPort})
			}
		}
	}
	for _, m := range in.Mounts {
		mount := engineMount{Type: m.Type, Source: m.Source, Target: m.Destination, ReadOnly: !m.RW}
		if m.Type == "volume" {
			mount.Source = m.Name
		}
		out.HostConfig.Mounts = append(out.HostConfig.Mounts, mount)
	}
	if p := in.HostConfig.RestartPolicy; p.Name != "" {
		out.HostConfig.RestartPolicy = &engineRestartPolicy{Name: p.Name, MaximumRetryCount: p.MaximumRetryCount}
	}

	mode := in.HostConfig.NetworkMode
	if mode == "default" || mode == "bridge" {
		mode = ""
	}
	out.HostConfig.NetworkMode = mode
	var networks []string
	for name, endpoint := range in.NetworkSettings.Networks {
		if builtinDockerNetwork(name) {
			continue
		}
		networks = append(networks, name)
		var aliases []string
		for _, alias := range endpoint.Aliases {
			// Docker adds the source container's own ID; it means nothing on the engine.
			if alias != "" && !strings.HasPrefix(in.ID, alias) {
				aliases = append(aliases, alias)
			}
		}
		if out.NetworkingConfig.EndpointsConfig == nil {
			out.NetworkingConfig.EndpointsConfig = map[string]engineEndpoint{}
		}
		out.NetworkingConfig.EndpointsConfig[name] = engineEndpoint{Aliases: aliases}
	}
	return strings.TrimPrefix(in.Name, "/"), out, networks, nil
}

// ensureEngineNetwork creates a user network on the engine like the source's.
func (r *vmfreeRuntime) ensureEngineNetwork(ctx context.Context, source, name string) error {
	if _, err := r.request(ctx, "GET", "/networks/"+url.PathEscape(name), nil); !engineNotFound(err) {
		return err
	}
	out, err := runDockerCLIAtEndpoint(30*time.Second, "", source, "network", "inspect", "--format", "{{json .}}", name)
	if err != nil {
		return fmt.Errorf("inspect network %s on the source: %w", name, err)
	}
	var network struct {
		Driver               string
		Labels               map[string]string
		Internal, Attachable bool
	}
	if err := json.Unmarshal([]byte(out), &network); err != nil {
		return err
	}
	_, err = r.request(ctx, "POST", "/networks/create", map[string]any{
		"Name": name, "Driver": network.Driver, "Labels": network.Labels, "Internal": network.Internal, "Attachable": network.Attachable,
	})
	if err != nil {
		return fmt.Errorf("create network %s: %w", name, err)
	}
	return nil
}

func (s *DockerService) migrateDockerContainersToEngine(r *vmfreeRuntime) (DockerCLIResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	source := s.dockerMigrationSourceEndpoint()
	sourceClient, err := s.dockerMigrationSourceClient()
	if err != nil {
		return DockerCLIResult{}, err
	}
	defer sourceClient.Close()
	list, err := sourceClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return DockerCLIResult{}, err
	}
	running := 0
	var stopped []string
	for _, item := range list.Items {
		if strings.EqualFold(string(item.State), "running") {
			running++
		} else {
			stopped = append(stopped, item.ID)
		}
	}
	progress := func(message string, done int, current string) {
		s.setDockerMigrationProgress(DockerMigrationStatus{Kind: "container", Message: message, TotalImages: len(stopped), ImportedImages: done, CurrentImage: current})
	}
	progress("Preparing stopped Docker containers.", 0, "")
	if len(stopped) == 0 {
		return DockerCLIResult{Output: "No stopped Docker containers to migrate."}, nil
	}

	existing := map[string]bool{}
	if containers, err := r.ListContainers(ctx); err != nil {
		return DockerCLIResult{}, fmt.Errorf("list Dockiva Engine containers before migration: %w", err)
	} else {
		for _, c := range containers {
			existing[c.Name] = true
		}
	}

	created, already := 0, 0
	var skipped []string
	networkErr := map[string]error{}
	for index, id := range stopped {
		label := shortenID(id)
		progress(fmt.Sprintf("Migrating container %d of %d…", index+1, len(stopped)), index, label)
		reason := func() string {
			inspect, err := sourceClient.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
			if err != nil {
				return "inspect failed: " + err.Error()
			}
			name, body, networks, err := engineCreateFromInspect(inspect.Raw)
			if err != nil {
				return "inspect parse failed: " + err.Error()
			}
			if name != "" {
				label = name
			}
			if existing[name] {
				already++
				return ""
			}
			imagePath := (&url.URL{Path: body.Image}).EscapedPath()
			if _, err := r.request(ctx, "GET", "/images/"+imagePath+"/json", nil); err != nil {
				return fmt.Sprintf("image %s is not in Dockiva Engine (migrate images first; only linux/arm64 images are copied)", body.Image)
			}
			for _, network := range networks {
				if _, done := networkErr[network]; !done {
					networkErr[network] = r.ensureEngineNetwork(ctx, source, network)
				}
				if err := networkErr[network]; err != nil {
					return err.Error()
				}
			}
			if _, err := r.request(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), body); err != nil {
				return err.Error()
			}
			created++
			return ""
		}()
		if reason != "" {
			skipped = append(skipped, fmt.Sprintf("%s: %s", label, reason))
		}
		progress(fmt.Sprintf("Processed container %d of %d", index+1, len(stopped)), index+1, label)
	}

	message := fmt.Sprintf("Migrated %d stopped Docker container(s) into Dockiva Engine.", created)
	if already > 0 {
		message += fmt.Sprintf(" %d already migrated.", already)
	}
	if running > 0 {
		message += fmt.Sprintf(" Skipped %d running container(s); stop them first.", running)
	}
	if len(skipped) > 0 {
		message += fmt.Sprintf(" Skipped %d: %s.", len(skipped), summarizeMigrationItems(skipped))
	}
	return DockerCLIResult{Output: message}, nil
}
