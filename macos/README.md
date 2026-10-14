# Dockiva for macOS (native)

A SwiftUI front end for Dockiva. Engine management, settings and Docker access
stay in Go: the app starts the Go core (`dockiva --core SOCKET`) and calls the
same services the Wails window binds, over a unix socket (see
[`core_api.go`](../core_api.go) for the protocol). The Wails app keeps shipping
until this one reaches parity.

```
Dockiva.app/Contents/
  MacOS/Dockiva          SwiftUI app
  MacOS/dockiva-core     Go core, started by the app; exits when the app does
  Resources/             vmfree-engine, dockiva-vmm, guest (from the Wails build)
```

## Build and run

```bash
macos/build-app.sh
```

```bash
open macos/build/Dockiva.app
```

The script copies the engine resources from `bin/dockiva.app` (a Wails build);
pass another `Contents/Resources` directory as its argument if needed. For
development, `DOCKIVA_CORE=/path/to/dockiva` makes the app run that core instead
of the bundled one. Core output goes to `~/Library/Logs/Dockiva/core.log`.

Tests: `swift test --package-path macos` (Swift client) and `go test -run CoreAPI .`
(the core API).

## Status

Done:

- Containers: list with live CPU and memory, published ports as links, filter,
  start, stop, restart, remove; logs and a line-mode terminal in an inspector.
- Images (pull, remove), volumes (create, remove, show in Finder), networks
  (create, remove).
- Engine: state, start and stop, endpoint and `DOCKER_HOST` hint.
- Menu bar item: running count, start and stop per container, open ports.
- Settings: start the engine on launch, reconnect automatically.
- View menu shortcuts: ⌘1–⌘5 for the sections, ⌘R to refresh.

Still in the Wails app only:

- Compose projects: grouping, up, down and restart, importing a compose file or folder.
- Container details (environment, mounts, networks).
- A full terminal emulator (the Wails app uses xterm.js; SwiftTerm is the native option).
- Engine settings: backend selection, external endpoints, deleting the engine.
- Migration from Docker Desktop or OrbStack, disk usage and prune, host container storage.
- Updates and start at login (Wails' updater and autostart; native: Sparkle or
  a GitHub release check, and `SMAppService`).
