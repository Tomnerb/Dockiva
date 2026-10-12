package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var vmfreeLaunchMu sync.Mutex

// The app ships this helper; no Python, shell, Docker or developer checkout is
// needed at runtime. The override is for source builds and isolated tests only.
func vmfreeHelperPath() (string, error) {
	if path := os.Getenv("DOCKIVA_VMFREE_HELPER"); path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("VM-free helper override must be absolute")
		}
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() && stat.Mode()&0111 != 0 {
			return path, nil
		}
		return "", errors.New("configured VM-free helper is missing or not executable")
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(executable)
	for _, path := range []string{
		filepath.Join(dir, "..", "Resources", "vmfree-engine", "vmfree-engine"),
		filepath.Join(dir, "vmfree-engine", "vmfree-engine"),
	} {
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() && stat.Mode()&0111 != 0 {
			return filepath.Clean(path), nil
		}
	}
	return "", errors.New("this Dockiva build is missing its bundled VM-free engine; install a complete build")
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
			return fmt.Errorf("VM-free engine exited (%v); see %s", err, logPath)
		case <-ticker.C:
			if probe() {
				return nil
			}
		case <-deadline.C:
			_ = cmd.Process.Kill()
			<-done
			return fmt.Errorf("VM-free engine startup timed out; see %s", logPath)
		}
	}
}
