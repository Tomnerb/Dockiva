//go:build darwin

package main

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	dockivaVMMGuestPort       = 10250
	dockivaVMMDockerGuestPort = 10251
	maxBundledGuestDiskSize   = 8 << 30
)

// nativeVMReadyTimeout bounds how long Start waits for Docker inside the guest.
var nativeVMReadyTimeout = 60 * time.Second

type darwinEngineBackend struct{}

func newPlatformEngineBackend() platformEngineBackend {
	return darwinEngineBackend{}
}

func (darwinEngineBackend) Name() string {
	return "vz"
}

func nativeRuntimeProvider() string {
	return "docker"
}

func (darwinEngineBackend) Options() []string {
	return []string{}
}

func (backend darwinEngineBackend) Status(
	service *DockerService,
	_ string,
) EngineStatus {
	status := EngineStatus{
		Backend:   "vz",
		Platform:  "darwin",
		Supported: true,
		Message:   "Native macOS Virtualization.framework backend.",
	}

	helper, err := dockivaVMMPath()
	if err != nil {
		status.Message = err.Error()
		return status
	}

	status.HelperInstalled = true

	guestDir, err := dockivaGuestDir()
	if err != nil {
		status.Message = err.Error()
		return status
	}

	kernel := filepath.Join(guestDir, "vmlinux")
	disk := filepath.Join(guestDir, "rootfs.ext4")
	bundledGuest := bundledGuestDir()
	bundledAssetsAvailable := bundledGuest != "" &&
		fileExists(filepath.Join(bundledGuest, "vmlinux")) &&
		bundledGuestDiskAvailable(bundledGuest)

	if fileExists(kernel) && fileExists(disk) || bundledAssetsAvailable {
		status.EngineInstalled = true
	} else {
		status.Message = fmt.Sprintf(
			"Native helper is installed, but guest assets are missing under %s",
			guestDir,
		)
		return status
	}

	stateDir, _ := dockivaVMMStateDir()
	proxySocket := filepath.Join(stateDir, "docker.sock")

	output, err := runCommand(
		5*time.Second,
		"",
		helper,
		"status",
		"--state-dir",
		stateDir,
	)
	if err == nil && strings.Contains(output, `"running":true`) {
		status.Running = true
		status.Endpoint = proxySocket
		status.Message = "Native Virtualization.framework VM with Docker-compatible API is running."
	} else {
		status.Message = "Native VM is prepared but not running."
	}

	return status
}

func (backend darwinEngineBackend) Start(
	service *DockerService,
	_ string,
) (EngineActionResult, error) {
	helper, err := dockivaVMMPath()
	if err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	if err := stageBundledGuestAssets(); err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, fmt.Errorf("stage bundled macOS guest assets: %w", err)
	}

	guestDir, err := dockivaGuestDir()
	if err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	kernel := filepath.Join(guestDir, "vmlinux")
	disk := filepath.Join(guestDir, "rootfs.ext4")

	if !fileExists(kernel) || !fileExists(disk) {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, fmt.Errorf(
			"macOS guest assets are missing; copy vmlinux and rootfs.ext4 into %s (see scripts/build-macos-guest-assets-linux.sh)",
			guestDir,
		)
	}

	stateDir, err := dockivaVMMStateDir()
	if err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	if status := backend.Status(service, ""); status.Running {
		if reachableDockerEndpoint("unix://" + status.Endpoint) {
			if err := service.ConfigureNativeDockerEndpoint("unix://" + status.Endpoint); err != nil {
				return EngineActionResult{Status: status}, err
			}
			if err := service.SelectContainerRuntime("docker"); err != nil {
				return EngineActionResult{Status: status}, err
			}
			return EngineActionResult{Status: status}, nil
		}
		// A VM whose Docker never answers (a guest that failed to boot) is replaced.
		_, _ = runCommand(15*time.Second, "", helper, "stop", "--state-dir", stateDir)
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	logDir, err := os.UserHomeDir()
	if err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	logDir = filepath.Join(logDir, "Library", "Logs", "Dockiva")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	logPath := filepath.Join(logDir, "vmm.log")
	logFile, err := os.OpenFile(
		logPath,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o644,
	)
	if err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}
	defer logFile.Close()

	cpuCount := defaultDarwinVMCPUCount()
	memoryMiB := defaultDarwinVMMemoryMiB()

	cmd := exec.Command(
		helper,
		"serve",
		"--kernel",
		kernel,
		"--disk",
		disk,
		"--state-dir",
		stateDir,
		"--cpu",
		strconv.Itoa(cpuCount),
		"--memory-mib",
		strconv.Itoa(memoryMiB),
		"--guest-port",
		strconv.Itoa(dockivaVMMGuestPort),
		"--docker-guest-port",
		strconv.Itoa(dockivaVMMDockerGuestPort),
	)

	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// The VMM creates docker.sock itself before the guest boots, so only an answer
	// from Docker inside the guest means the engine is up.
	proxySocket := filepath.Join(stateDir, "docker.sock")
	var lastErr error
	for deadline := time.Now().Add(nativeVMReadyTimeout); time.Now().Before(deadline); {
		select {
		case err := <-exited:
			return EngineActionResult{
				Status: backend.Status(service, ""),
			}, fmt.Errorf("native VM exited during startup (%v); see %s", err, logPath)
		default:
		}

		if fileExists(proxySocket) && reachableDockerEndpoint("unix://"+proxySocket) {
			if err := service.ConfigureNativeDockerEndpoint("unix://" + proxySocket); err != nil {
				lastErr = err
			} else if err := service.SelectContainerRuntime("docker"); err != nil {
				lastErr = err
			} else {
				status := backend.Status(service, "")
				status.Running = true
				status.Endpoint = proxySocket

				return EngineActionResult{
					Status: status,
					Output: fmt.Sprintf(
						"Native macOS VM started with %d CPU(s), %d MiB RAM.",
						cpuCount,
						memoryMiB,
					),
				}, nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	// A guest that never brings Docker up must not keep its vCPUs busy.
	_, _ = runCommand(15*time.Second, "", helper, "stop", "--state-dir", stateDir)
	if lastErr == nil {
		lastErr = errors.New("Docker did not answer")
	}
	return EngineActionResult{
		Status: backend.Status(service, ""),
	}, fmt.Errorf(
		"native VM did not become ready within %s and was stopped (%w); see %s",
		nativeVMReadyTimeout,
		lastErr,
		logPath,
	)
}

func (backend darwinEngineBackend) Stop(
	service *DockerService,
	_ string,
) (EngineActionResult, error) {
	helper, err := dockivaVMMPath()
	if err != nil {
		return EngineActionResult{
			Status: backend.Status(service, ""),
		}, err
	}

	stateDir, _ := dockivaVMMStateDir()

	output, err := runCommand(
		15*time.Second,
		"",
		helper,
		"stop",
		"--state-dir",
		stateDir,
	)

	if err == nil {
		service.useOfflineNativeRuntime(backend.Status(service, ""))
	}

	return EngineActionResult{
		Status: backend.Status(service, ""),
		Output: output,
	}, err
}

func (backend darwinEngineBackend) Delete(
	service *DockerService,
	_ string,
) (EngineActionResult, error) {
	status := backend.Status(service, "")

	if status.Running {
		return EngineActionResult{
			Status: status,
		}, errors.New("stop the native VM before deleting its guest disk")
	}

	guestDir, err := dockivaGuestDir()
	if err != nil {
		return EngineActionResult{Status: status}, err
	}

	disk := filepath.Join(guestDir, "rootfs.ext4")
	if !fileExists(disk) {
		return EngineActionResult{Status: status}, nil
	}

	if err := os.Remove(disk); err != nil {
		return EngineActionResult{Status: status}, err
	}

	return EngineActionResult{
		Status: backend.Status(service, ""),
		Output: "Removed Dockiva macOS guest rootfs.ext4. Kernel asset was preserved.",
	}, nil
}

func (backend darwinEngineBackend) Provision(
	service *DockerService,
	option string,
) (EngineActionResult, error) {
	return EngineActionResult{
		Status: backend.Status(service, option),
	}, errors.New(
		"macOS native guest assets are prepared outside the running app; use scripts/build-macos-guest-assets-linux.sh and scripts/install-macos-native-assets.sh",
	)
}

func dockivaVMMPath() (string, error) {
	if explicit := firstNonEmptyEnvironment("DOCKIVA_VMM_PATH", "DEVSTACK_VMM_PATH"); explicit != "" {
		if fileExists(explicit) {
			return explicit, nil
		}
	}

	executable, _ := os.Executable()
	if executable != "" {
		candidates := []string{
			filepath.Join(
				filepath.Dir(executable),
				"..",
				"Resources",
				"dockiva-vmm",
			),
			filepath.Join(
				filepath.Dir(executable),
				"dockiva-vmm",
			),
		}

		for _, candidate := range candidates {
			candidate = filepath.Clean(candidate)
			if fileExists(candidate) {
				return candidate, nil
			}
		}
	}

	home, _ := os.UserHomeDir()

	candidates := []string{
		"/usr/local/libexec/dockiva-vmm",
		"/opt/homebrew/libexec/dockiva-vmm",
		filepath.Join(
			home,
			".local",
			"libexec",
			"dockiva-vmm",
		),
		filepath.Join(
			"native",
			"macos",
			"DockivaVMM",
			".build",
			"release",
			"dockiva-vmm",
		),
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", errors.New(
		"native macOS VMM helper was not found; build it with scripts/build-macos-vmm.sh",
	)
}

func dockivaGuestDir() (string, error) {
	if explicit := firstNonEmptyEnvironment("DOCKIVA_GUEST_DIR", "DEVSTACK_GUEST_DIR"); explicit != "" {
		return filepath.Clean(explicit), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	dockivaDir := filepath.Join(
		home,
		"Library",
		"Application Support",
		"Dockiva",
		"guest",
	)
	legacyDir := filepath.Join(
		home,
		"Library",
		"Application Support",
		"DevStack",
		"guest",
	)
	if !fileExists(filepath.Join(dockivaDir, "rootfs.ext4")) &&
		fileExists(filepath.Join(legacyDir, "rootfs.ext4")) &&
		fileExists(filepath.Join(legacyDir, "vmlinux")) {
		return legacyDir, nil
	}

	return dockivaDir, nil
}

func bundledGuestDir() string {
	if explicit := firstNonEmptyEnvironment("DOCKIVA_GUEST_ASSETS_PATH", "DEVSTACK_GUEST_ASSETS_PATH"); explicit != "" {
		return filepath.Clean(explicit)
	}

	executable, err := os.Executable()
	if err != nil || executable == "" {
		return ""
	}

	return filepath.Clean(filepath.Join(
		filepath.Dir(executable),
		"..",
		"Resources",
		"guest",
	))
}

func bundledGuestDiskAvailable(directory string) bool {
	return fileExists(filepath.Join(directory, "rootfs.ext4")) ||
		fileExists(filepath.Join(directory, "rootfs.ext4.gz"))
}

// stageBundledGuestAssets copies immutable assets out of the signed app bundle
// before first use. The guest rootfs is writable, so running it directly from
// Contents/Resources would invalidate the bundle and may fail on read-only
// installations.
func stageBundledGuestAssets() error {
	destination, err := dockivaGuestDir()
	if err != nil {
		return err
	}

	kernelDestination := filepath.Join(destination, "vmlinux")
	diskDestination := filepath.Join(destination, "rootfs.ext4")
	if fileExists(kernelDestination) && fileExists(diskDestination) {
		return nil
	}

	source := bundledGuestDir()
	if source == "" ||
		!fileExists(filepath.Join(source, "vmlinux")) ||
		!bundledGuestDiskAvailable(source) {
		return nil
	}

	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}

	for _, name := range []string{"vmlinux", "manifest.txt"} {
		sourcePath := filepath.Join(source, name)
		destinationPath := filepath.Join(destination, name)
		if !fileExists(sourcePath) || fileExists(destinationPath) {
			continue
		}
		if err := copyFileAtomically(sourcePath, destinationPath); err != nil {
			return err
		}
	}

	if !fileExists(diskDestination) {
		compressedDisk := filepath.Join(source, "rootfs.ext4.gz")
		if fileExists(compressedDisk) {
			if err := decompressGzipFileAtomically(compressedDisk, diskDestination); err != nil {
				return err
			}
		} else if err := copyFileAtomically(filepath.Join(source, "rootfs.ext4"), diskDestination); err != nil {
			return err
		}
	}

	return nil
}

func decompressGzipFileAtomically(source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	compressed, err := gzip.NewReader(input)
	if err != nil {
		return fmt.Errorf("open compressed guest disk: %w", err)
	}
	defer compressed.Close()

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".dockiva-asset-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	written, copyErr := io.Copy(temporary, io.LimitReader(compressed, maxBundledGuestDiskSize+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("decompress guest disk: %w", copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxBundledGuestDiskSize {
		return fmt.Errorf("compressed guest disk exceeds %d bytes", maxBundledGuestDiskSize)
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func copyFileAtomically(source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".dockiva-asset-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := io.Copy(temporary, input); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	return os.Rename(temporaryPath, destination)
}

func dockivaVMMStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	current := filepath.Join(
		home,
		"Library",
		"Application Support",
		"Dockiva",
		"run",
	)
	legacy := filepath.Join(
		home,
		"Library",
		"Application Support",
		"DevStack",
		"run",
	)

	// A rename update can leave the DevStack VM running. Continue using that
	// state directory so Dockiva reconnects to the existing VM instead of
	// attaching its already-mounted guest disk to a second VM.
	if darwinVMMStateIsRunning(current) {
		return current, nil
	}
	if darwinVMMStateIsRunning(legacy) {
		return legacy, nil
	}

	return current, nil
}

func darwinVMMStateIsRunning(stateDir string) bool {
	pidBytes, err := os.ReadFile(filepath.Join(stateDir, "vmm.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || pid <= 0 {
		return false
	}

	var status struct {
		Running bool `json:"running"`
		PID     int  `json:"pid"`
	}
	statusBytes, err := os.ReadFile(filepath.Join(stateDir, "status.json"))
	if err != nil || json.Unmarshal(statusBytes, &status) != nil ||
		!status.Running || status.PID != pid {
		return false
	}

	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func defaultDarwinVMCPUCount() int {
	value := firstNonEmptyEnvironment("DOCKIVA_VM_CPUS", "DEVSTACK_VM_CPUS")
	if value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 && parsed <= 16 {
			return parsed
		}
	}

	return 4
}

func defaultDarwinVMMemoryMiB() int {
	value := firstNonEmptyEnvironment("DOCKIVA_VM_MEMORY_MIB", "DEVSTACK_VM_MEMORY_MIB")
	if value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 512 && parsed <= 32768 {
			return parsed
		}
	}

	return 2048
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstNonEmptyEnvironment(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
