package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// DOCKIVA_LIVE=1: against the running Dockiva Engine, create a container from an
// image the engine lacks (so CreateContainer pulls it), with an env var, then remove it.
func TestLiveCreateContainer(t *testing.T) {
	if os.Getenv("DOCKIVA_LIVE") == "" {
		t.Skip("set DOCKIVA_LIVE=1")
	}
	s := &DockerService{}
	s.replaceRuntime(newVMFreeRuntime())
	c, err := s.CreateContainer(RuntimeCreateContainerRequest{Name: "dkv-live-create", Image: "busybox:1.36.1",
		Command: []string{"sh", "-c", "echo $GREETING; sleep 30"}, Env: []string{"GREETING=hello-env"}, AutoStart: true,
		PortMappings: []RuntimePortMapping{{HostPort: 18555, ContainerPort: 80, Protocol: "tcp", HostIP: "127.0.0.1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.currentRuntime().RemoveContainer(context.Background(), c.ID, true)
	data, err := newVMFreeRuntime().request(context.Background(), "GET", vmfreeContainerPath(c.ID)+"/json", nil)
	if err != nil || !strings.Contains(string(data), "GREETING=hello-env") || !strings.Contains(string(data), "18555") {
		t.Fatalf("inspect: %v %.400s", err, data)
	}
}
