package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type runtimeTerminalBinding struct {
	provider  runtimeTerminalProvider
	runtimeID string
	cancel    context.CancelFunc
}

var runtimeTerminalBindings = struct {
	sync.Mutex
	items map[string]runtimeTerminalBinding
}{items: make(map[string]runtimeTerminalBinding)}

func putRuntimeTerminalBinding(sessionID string, binding runtimeTerminalBinding) {
	runtimeTerminalBindings.Lock()
	runtimeTerminalBindings.items[sessionID] = binding
	runtimeTerminalBindings.Unlock()
}

func getRuntimeTerminalBinding(sessionID string) (runtimeTerminalBinding, bool) {
	runtimeTerminalBindings.Lock()
	binding, ok := runtimeTerminalBindings.items[sessionID]
	runtimeTerminalBindings.Unlock()
	return binding, ok
}

func deleteRuntimeTerminalBinding(sessionID string) (runtimeTerminalBinding, bool) {
	runtimeTerminalBindings.Lock()
	binding, ok := runtimeTerminalBindings.items[sessionID]
	if ok {
		delete(runtimeTerminalBindings.items, sessionID)
	}
	runtimeTerminalBindings.Unlock()
	return binding, ok
}

func (s *DockerService) startRuntimeTerminal(containerID string) (string, error) {
	provider, ok := s.currentRuntime().(runtimeTerminalProvider)
	if !ok {
		return "", fmt.Errorf("the active runtime does not support terminal exec")
	}

	sessionID, err := newSessionID()
	if err != nil {
		return "", err
	}

	ctx, cancelCall := context.WithTimeout(context.Background(), 15*time.Second)
	runtimeID, err := provider.RuntimeOpenTerminal(ctx, containerID, 120, 28)
	cancelCall()
	if err != nil {
		return "", err
	}

	pollCtx, pollCancel := context.WithCancel(context.Background())

	session := &terminalSession{
		id:          sessionID,
		containerID: containerID,
	}

	s.sessionMu.Lock()
	s.terminalSessions[sessionID] = session
	s.sessionMu.Unlock()

	putRuntimeTerminalBinding(sessionID, runtimeTerminalBinding{
		provider:  provider,
		runtimeID: runtimeID,
		cancel:    pollCancel,
	})

	go s.readRuntimeTerminal(pollCtx, session, provider, runtimeID)

	return sessionID, nil
}

func (s *DockerService) readRuntimeTerminal(
	ctx context.Context,
	session *terminalSession,
	provider runtimeTerminalProvider,
	runtimeID string,
) {
	var offset int64
	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()

	defer func() {
		deleteRuntimeTerminalBinding(session.id)

		s.sessionMu.Lock()
		if s.terminalSessions[session.id] == session {
			delete(s.terminalSessions, session.id)
		}
		s.sessionMu.Unlock()

		if session.closed.CompareAndSwap(false, true) {
			s.emitStreamEvent(
				terminalOutputEvent,
				StreamOutputEvent{
					StreamID:    session.id,
					ContainerID: session.containerID,
					Closed:      true,
					Seq:         session.seq.Add(1),
				},
			)
		}
	}()

	for {
		chunk, err := provider.RuntimeTerminalOutput(ctx, runtimeID, offset)
		if err != nil {
			if ctx.Err() == nil && !session.closed.Load() {
				s.emitStreamEvent(
					terminalOutputEvent,
					StreamOutputEvent{
						StreamID:    session.id,
						ContainerID: session.containerID,
						Error:       err.Error(),
						Seq:         session.seq.Add(1),
					},
				)
			}
		} else {
			offset = chunk.NextOffset

			if chunk.Data != "" && !session.closed.Load() {
				s.emitStreamEvent(
					terminalOutputEvent,
					StreamOutputEvent{
						StreamID:    session.id,
						ContainerID: session.containerID,
						Data:        []byte(chunk.Data),
						Seq:         session.seq.Add(1),
					},
				)
			}

			if chunk.Error != "" && !session.closed.Load() {
				s.emitStreamEvent(
					terminalOutputEvent,
					StreamOutputEvent{
						StreamID:    session.id,
						ContainerID: session.containerID,
						Error:       chunk.Error,
						Seq:         session.seq.Add(1),
					},
				)
			}

			if chunk.Closed {
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *DockerService) startRuntimeLogStream(containerID string, tail int) (string, error) {
	provider, ok := s.currentRuntime().(runtimeLogProvider)
	if !ok {
		return "", fmt.Errorf("the active runtime does not support logs")
	}

	streamID, err := newSessionID()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithCancel(context.Background())

	session := &logStreamSession{
		id:          streamID,
		containerID: containerID,
		cancel:      cancel,
	}

	s.sessionMu.Lock()
	s.logStreams[streamID] = session
	s.sessionMu.Unlock()

	go s.readRuntimeLogStream(ctx, session, provider, tail)

	return streamID, nil
}

func (s *DockerService) readRuntimeLogStream(
	ctx context.Context,
	session *logStreamSession,
	provider runtimeLogProvider,
	tail int,
) {
	var offset int64
	first := true
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()

	defer func() {
		s.sessionMu.Lock()
		if s.logStreams[session.id] == session {
			delete(s.logStreams, session.id)
		}
		s.sessionMu.Unlock()

		if session.closed.CompareAndSwap(false, true) {
			s.emitStreamEvent(
				logOutputEvent,
				StreamOutputEvent{
					StreamID:    session.id,
					ContainerID: session.containerID,
					Closed:      true,
					Seq:         session.seq.Add(1),
				},
			)
		}
	}()

	for {
		requestTail := 0
		if first {
			requestTail = tail
			first = false
		}

		chunk, err := provider.RuntimeReadLogs(
			ctx,
			session.containerID,
			requestTail,
			offset,
		)

		if err != nil {
			if ctx.Err() == nil && !session.closed.Load() {
				s.emitStreamEvent(
					logOutputEvent,
					StreamOutputEvent{
						StreamID:    session.id,
						ContainerID: session.containerID,
						Error:       err.Error(),
						Seq:         session.seq.Add(1),
					},
				)
			}
		} else {
			offset = chunk.NextOffset

			if chunk.Data != "" && !session.closed.Load() {
				s.emitStreamEvent(
					logOutputEvent,
					StreamOutputEvent{
						StreamID:    session.id,
						ContainerID: session.containerID,
						Data:        []byte(chunk.Data),
						Seq:         session.seq.Add(1),
					},
				)
			}

			if chunk.Closed {
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
