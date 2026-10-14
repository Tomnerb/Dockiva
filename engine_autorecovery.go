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
		if startEngine {
			r := newVMFreeRuntime()
			defer r.Close()
			if err := startVMFreeEngine(r); err != nil {
				log.Printf("VM-free startup: %v", err)
			} else {
				s.emitEngineRecovered("vmfree-started")
			}
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
