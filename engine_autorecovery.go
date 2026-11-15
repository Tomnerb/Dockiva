package main

import (
	"log"
	"strings"
)

func (s *DockerService) AutoRecoverEngine(
	backend string,
	platformOption string,
	autoReconnect bool,
	startEngine bool,
) {
	backend = normalizeEngineBackend(backend)
	if backend == "vmfree" {
		r := newVMFreeRuntime()
		defer r.Close()
		if startEngine {
			if err := startVMFreeEngine(r); err != nil {
				log.Printf("Dockiva Engine startup: %v", err)
				return
			}
			s.emitEngineRecovered("vmfree-started")
		}
		// Chosen engine, so the docker CLI follows it even when launch does not start it.
		if err := useDockivaCLIContext(r.home); err != nil {
			log.Printf("docker CLI context: %v", err)
		}
		return
	}

	if backend == "external" && autoReconnect {
		if err := s.ReconnectExternalDocker(); err == nil {
			s.emitEngineRecovered(
				"docker-reconnected",
			)
			return
		}
	}

	if backend == "external" || !startEngine {
		return
	}

	if tryPlatformAutomaticEngineStart(
		s,
		backend,
		platformOption,
	) {
		s.emitEngineRecovered(
			"engine-started",
		)
		return
	}

	log.Printf(
		"Dockiva engine auto-recovery could not start backend %q",
		strings.TrimSpace(backend),
	)
}

func (s *DockerService) emitEngineRecovered(
	action string,
) {
	emitEvent(
		dockerChangedEvent,
		DockerEventNotice{
			Type:   "engine",
			Action: action,
		},
	)
}
