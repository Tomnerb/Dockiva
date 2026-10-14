package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Dockiva Engine (the VM-free runtime) has its own transport: never consult Docker
// contexts or fall back to Docker. The type keeps the engine's original name, as do
// the "vmfree" backend and provider values stored in settings.
type vmfreeRuntime struct {
	home string
	http *http.Client
}

func newVMFreeRuntime() *vmfreeRuntime { return vmfreeAt(engineHome()) }

// engineHome is the engine's state directory: ~/.dockiva-engine, or ~/.vmfree from
// before the rename until startVMFreeEngine moves it (migrateLegacyHome).
func engineHome() string {
	for _, name := range []string{"DOCKIVA_ENGINE_HOME", "VMFREE_HOME"} {
		if home := os.Getenv(name); home != "" {
			return home
		}
	}
	userHome, _ := os.UserHomeDir()
	current, legacy := filepath.Join(userHome, ".dockiva-engine"), filepath.Join(userHome, ".vmfree")
	if _, err := os.Lstat(current); err != nil {
		if st, err := os.Lstat(legacy); err == nil && st.IsDir() { // a real directory, not the compatibility link
			return legacy
		}
	}
	return current
}
func vmfreeAt(home string) *vmfreeRuntime {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", filepath.Join(home, "engine.sock"))
	}}
	return &vmfreeRuntime{home: home, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Dockiva Engine redirects are unsupported")
	}}}
}
func (r *vmfreeRuntime) Close() error { r.http.CloseIdleConnections(); return nil }
func (r *vmfreeRuntime) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	// The caller's deadline wins, since an image pull can take minutes; requests
	// without one keep the 25-second limit.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
	}
	// Read the token on each request so an engine restart does not require restarting Dockiva.
	data, err := os.ReadFile(filepath.Join(r.home, "docker-client", "config.json"))
	if err != nil {
		return nil, fmt.Errorf("Dockiva Engine is not ready: %w", err)
	}
	var config struct{ HttpHeaders map[string]string }
	if err = json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	token := config.HttpHeaders["X-VMFree-Token"]
	if len(token) != 64 {
		return nil, errors.New("invalid Dockiva Engine authentication configuration")
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://vmfree/v1.45"+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-VMFree-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16*1024*1024 {
		return nil, errors.New("Dockiva Engine response exceeds 16 MiB")
	}
	if resp.StatusCode >= 300 {
		var failure struct{ Message string }
		_ = json.Unmarshal(data, &failure)
		return nil, fmt.Errorf("Dockiva Engine HTTP %d: %s", resp.StatusCode, failure.Message)
	}
	return data, nil
}
func (r *vmfreeRuntime) Info(ctx context.Context) ContainerRuntimeInfo {
	info := ContainerRuntimeInfo{Provider: "vmfree", DisplayName: "Dockiva Engine", Endpoint: "unix://" + filepath.Join(r.home, "engine.sock"), Experimental: true,
		Capabilities: RuntimeCapabilities{Containers: true, Lifecycle: true, Logs: true, Images: true, PullImages: true, CreateContainer: true, PortPublishing: true},
		Message:      "Internal testing of linux/arm64 images pulled from registries. Snapshot logs; TCP ports publish on 127.0.0.1. No builds, terminal, volumes, Compose or resource statistics. Not production-qualified."}
	_, err := r.request(ctx, "GET", "/_ping", nil)
	info.Connected = err == nil
	if err != nil {
		info.Message = err.Error()
	}
	return info
}
func vmfreeCandidate() RuntimeCandidateInfo {
	r := newVMFreeRuntime()
	defer r.Close()
	_, err := os.Stat(filepath.Join(r.home, "engine.sock"))
	supported := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	return RuntimeCandidateInfo{Provider: "vmfree", DisplayName: "Dockiva Engine", Available: supported, Selectable: supported, Detected: err == nil, Experimental: true, Endpoint: "unix://" + filepath.Join(r.home, "engine.sock"), Message: "Runs Linux containers natively on Apple Silicon, with no virtual machine. Starts automatically when selected."}
}
func (r *vmfreeRuntime) ListContainers(ctx context.Context) ([]ContainerInfo, error) {
	data, err := r.request(ctx, "GET", "/containers/json?all=1", nil)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Id, Image, State, Status string
		Names                    []string
		Ports                    []struct {
			PrivatePort, PublicPort uint16
			Type                    string
		}
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	result := make([]ContainerInfo, 0, len(rows))
	for _, row := range rows {
		ports := []PortInfo{}
		for _, p := range row.Ports {
			ports = append(ports, PortInfo{PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type,
				Display: fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, p.Type), URL: localhostURL(p.PublicPort, p.PrivatePort, p.Type)})
		}
		result = append(result, ContainerInfo{ID: row.Id, ShortID: shortenID(row.Id), Name: strings.TrimPrefix(firstContainerName(row.Names), "/"), Image: row.Image, State: row.State, Status: row.Status, Ports: ports})
	}
	return result, nil
}
func vmfreeContainerPath(id string) string { return "/containers/" + url.PathEscape(id) }
func (r *vmfreeRuntime) StartContainer(ctx context.Context, id string) error {
	_, err := r.request(ctx, "POST", vmfreeContainerPath(id)+"/start", nil)
	return err
}
func (r *vmfreeRuntime) StopContainer(ctx context.Context, id string, _ time.Duration) error {
	_, err := r.request(ctx, "POST", vmfreeContainerPath(id)+"/stop", nil)
	return err
}
func (r *vmfreeRuntime) RestartContainer(ctx context.Context, id string, _ time.Duration) error {
	_, err := r.request(ctx, "POST", vmfreeContainerPath(id)+"/restart", nil)
	return err
}
func (r *vmfreeRuntime) RemoveContainer(ctx context.Context, id string, force bool) error {
	_, err := r.request(ctx, "DELETE", vmfreeContainerPath(id)+fmt.Sprintf("?force=%t", force), nil)
	return err
}
func (r *vmfreeRuntime) Inspect(ctx context.Context, id string) ([]byte, error) {
	return r.request(ctx, "GET", vmfreeContainerPath(id)+"/json", nil)
}
func (r *vmfreeRuntime) ListRuntimeImages(ctx context.Context) ([]ImageInfo, error) {
	data, err := r.request(ctx, "GET", "/images/json", nil)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Id            string
		RepoTags      []string
		Size, Created int64
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	result := make([]ImageInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, ImageInfo{ID: row.Id, ShortID: shortenID(row.Id), Tags: row.RepoTags, Size: row.Size, Created: row.Created})
	}
	return result, nil
}
func (r *vmfreeRuntime) PullRuntimeImage(ctx context.Context, reference string) (RuntimeOperationResult, error) {
	// The engine applies Docker naming: "alpine:3.20" means docker.io/library/alpine:3.20.
	data, err := r.request(ctx, "POST", "/images/create?fromImage="+url.QueryEscape(reference), nil)
	if err != nil {
		return RuntimeOperationResult{}, err
	}
	var status struct{ Status string }
	_ = json.Unmarshal(data, &status)
	return RuntimeOperationResult{Message: "Pulled " + reference + ".", Detail: status.Status}, nil
}
func (r *vmfreeRuntime) CreateRuntimeContainer(ctx context.Context, q RuntimeCreateContainerRequest) (ContainerInfo, error) {
	// The engine publishes TCP on 127.0.0.1 and rejects anything else.
	bindings := map[string]any{}
	for _, m := range q.PortMappings {
		if m.Protocol != "" && m.Protocol != "tcp" {
			return ContainerInfo{}, errors.New("Dockiva Engine publishes TCP ports only")
		}
		bindings[fmt.Sprintf("%d/tcp", m.ContainerPort)] = []map[string]string{{"HostIp": "127.0.0.1", "HostPort": fmt.Sprint(m.HostPort)}}
	}
	// Like Docker, every container gets a private writable root.
	body := map[string]any{"Image": q.Image, "HostConfig": map[string]any{"NetworkMode": "host", "PortBindings": bindings, "ReadonlyRootfs": false}}
	if len(q.Command) > 0 {
		body["Cmd"] = q.Command
	}
	data, err := r.request(ctx, "POST", "/containers/create?name="+url.QueryEscape(q.Name), body)
	if err != nil {
		return ContainerInfo{}, err
	}
	var created struct{ Id string }
	if err = json.Unmarshal(data, &created); err != nil {
		return ContainerInfo{}, err
	}
	item := ContainerInfo{ID: created.Id, ShortID: shortenID(created.Id), Name: q.Name, Image: q.Image, State: "created", Ports: []PortInfo{}}
	if q.AutoStart {
		if err = r.StartContainer(ctx, created.Id); err != nil {
			return item, fmt.Errorf("created %s, but start failed: %w", q.Name, err)
		}
		item.State = "running"
	}
	return item, nil
}
func (r *vmfreeRuntime) RuntimeReadLogs(ctx context.Context, id string, tail int, _ int64) (RuntimeLogChunk, error) {
	suffix := "all"
	if tail > 0 {
		suffix = fmt.Sprint(tail)
	}
	data, err := r.request(ctx, "GET", vmfreeContainerPath(id)+"/logs?stdout=1&stderr=1&tail="+suffix, nil)
	if err != nil {
		return RuntimeLogChunk{}, err
	}
	var out bytes.Buffer
	for len(data) > 0 {
		if len(data) < 8 {
			return RuntimeLogChunk{}, errors.New("truncated Dockiva Engine log frame")
		}
		n := int(binary.BigEndian.Uint32(data[4:8]))
		data = data[8:]
		if n > len(data) {
			return RuntimeLogChunk{}, errors.New("truncated Dockiva Engine log payload")
		}
		out.Write(data[:n])
		data = data[n:]
	}
	return RuntimeLogChunk{Data: out.String(), NextOffset: int64(out.Len()), Closed: true}, nil
}
func (r *vmfreeRuntime) ListRuntimeVolumes(context.Context) ([]VolumeInfo, error) {
	return []VolumeInfo{}, nil
}
func (r *vmfreeRuntime) ListRuntimeNetworks(context.Context) ([]NetworkInfo, error) {
	return []NetworkInfo{}, nil
}
