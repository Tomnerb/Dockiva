package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Choosing Dockiva Engine also points the docker CLI, and the tools that follow
// its context (Compose, buildx, act…), at the engine, the way Docker Desktop and
// OrbStack take the context when they start. The context it replaced is saved and
// given back when another engine is chosen.
const dockivaCLIContext = "dockiva"

func dockerCLI(args ...string) (string, error) {
	path, err := dockerCLIPath()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	// The app sets DOCKER_HOST for itself; the CLI must see the user's own context.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DOCKER_HOST=") && !strings.HasPrefix(kv, "DOCKER_CONTEXT=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func currentCLIContext() (string, error) {
	return dockerCLI("context", "inspect", "--format", "{{.Name}}")
}

func previousCLIContextPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Dockiva", "previous-docker-context"), nil
}

// useDockivaCLIContext creates or repairs the "dockiva" context for the engine at
// home and makes it the docker CLI's current context.
func useDockivaCLIContext(home string) error {
	current, err := currentCLIContext()
	if err != nil {
		return err
	}
	verb := "update"
	if _, err := dockerCLI("context", "inspect", dockivaCLIContext); err != nil {
		verb = "create"
	}
	host := "host=unix://" + filepath.Join(home, "docker.sock")
	if _, err := dockerCLI("context", verb, dockivaCLIContext, "--description", "Dockiva Engine", "--docker", host); err != nil {
		return err
	}
	if current == dockivaCLIContext {
		return nil
	}
	path, err := previousCLIContextPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(current+"\n"), 0600); err != nil {
		return err
	}
	_, err = dockerCLI("context", "use", dockivaCLIContext)
	return err
}

// restoreDockerCLIContext gives back the context useDockivaCLIContext replaced,
// unless the user has switched contexts since. It runs no docker command when
// nothing was replaced.
func restoreDockerCLIContext() error {
	path, err := previousCLIContextPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	current, err := currentCLIContext()
	if err != nil {
		return err
	}
	if current == dockivaCLIContext {
		previous := strings.TrimSpace(string(data))
		if _, err := dockerCLI("context", "inspect", previous); previous == "" || err != nil {
			previous = "default" // the saved context was removed since
		}
		if _, err := dockerCLI("context", "use", previous); err != nil {
			return err
		}
	}
	return os.Remove(path)
}
