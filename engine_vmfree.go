package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var vmfreeLaunchMu sync.Mutex

// The app ships this helper; no Python, shell, Docker or developer checkout is
// needed at runtime. The override is for source builds and isolated tests only.
func vmfreeHelperPath() (string, error) {
	path := os.Getenv("DOCKIVA_ENGINE_HELPER")
	if path == "" {
		path = os.Getenv("DOCKIVA_VMFREE_HELPER")
	}
	if path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("Dockiva Engine helper override must be absolute")
		}
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() && stat.Mode()&0111 != 0 {
			return path, nil
		}
		return "", errors.New("configured Dockiva Engine helper is missing or not executable")
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(executable)
	for _, path := range []string{
		filepath.Join(dir, "..", "Resources", "dockiva-engine", "dockiva-engine"),
		filepath.Join(dir, "dockiva-engine", "dockiva-engine"),
		filepath.Join(dir, "..", "Resources", "vmfree-engine", "vmfree-engine"), // builds from before the rename
	} {
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() && stat.Mode()&0111 != 0 {
			return filepath.Clean(path), nil
		}
	}
	return "", errors.New("this Dockiva build is missing its bundled Dockiva Engine; install a complete build")
}

func startVMFreeEngine(r *vmfreeRuntime) error {
	vmfreeLaunchMu.Lock()
	defer vmfreeLaunchMu.Unlock()
	probe := func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return r.Info(ctx).Connected
	}
	if probe() {
		return nil
	}
	helper, err := vmfreeHelperPath()
	if err != nil {
		return err
	}
	migrateLegacyHome(r)
	if err = os.MkdirAll(r.home, 0700); err != nil {
		return err
	}
	logPath := filepath.Join(r.home, "dockiva-engine.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	cmd := exec.Command(helper, "--home", r.home)
	cmd.Stdout = log
	cmd.Stderr = log
	// No console input; the engine and supervisors survive UI close/reopen.
	err = cmd.Start()
	log.Close()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if probe() {
				return nil
			} // A second app instance may have won the engine lock.
			return fmt.Errorf("Dockiva Engine exited (%v); see %s", err, logPath)
		case <-ticker.C:
			if probe() {
				return nil
			}
		case <-deadline.C:
			_ = cmd.Process.Kill()
			<-done
			return fmt.Errorf("Dockiva Engine startup timed out; see %s", logPath)
		}
	}
}

// migrateLegacyHome moves ~/.vmfree to ~/.dockiva-engine once nothing uses it: the
// engine is not answering (the caller checked) and no engine, supervisor or
// container process names the directory. Container state holds no absolute paths,
// so a rename keeps it intact. A link stays at the old path for DOCKER_HOST
// settings and runtimes still pointing there.
func migrateLegacyHome(r *vmfreeRuntime) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return
	}
	legacy, current := filepath.Join(userHome, ".vmfree"), filepath.Join(userHome, ".dockiva-engine")
	if r.home != legacy {
		return
	}
	if _, err := os.Lstat(current); err == nil || homeInUse(legacy) {
		return
	}
	if err := os.Rename(legacy, current); err != nil {
		log.Printf("Dockiva Engine: keeping %s: %v", legacy, err)
		return
	}
	if err := os.Symlink(current, legacy); err != nil {
		log.Printf("Dockiva Engine: moved %s to %s; no compatibility link: %v", legacy, current, err)
	}
	*r = *vmfreeAt(current)
}

// homeInUse reports whether a process's arguments name home (or the process list
// cannot be read). ponytail: argument matching, not open files; supervisors and
// runtimes always pass their container paths as arguments.
func homeInUse(home string) bool {
	out, err := exec.Command("/bin/ps", "-axww", "-o", "command=").Output()
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, home+"/") || strings.Contains(line, home+" ") || strings.HasSuffix(line, home) {
			return true
		}
	}
	return false
}
