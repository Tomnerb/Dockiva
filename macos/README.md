# Dockiva for macOS (native)

A SwiftUI front end for Dockiva. Engine management, settings and Docker access
stay in Go: the app starts the Go core (`dockiva --core SOCKET`) and calls the
same services the Wails window binds, over a unix socket (see
[`core_api.go`](../core_api.go) for the protocol). Since v0.5 it is the app macOS
releases ship (macOS 14 or later); Windows and Linux keep the Wails app.

```
Dockiva.app/Contents/
  MacOS/Dockiva          SwiftUI app
  MacOS/dockiva-core     Go core, started by the app; exits when the app does
  Resources/             dockiva-engine, dockiva-vmm, guest (from the Wails build)
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

## Releases

`scripts/build-macos.sh` builds the Wails bundle for its Go program and
resources, then makes it this app: the Go executable becomes
`Contents/MacOS/dockiva-core`, the Swift build becomes `Contents/MacOS/Dockiva`,
and `Info.plist` points at it (same bundle identifier, so v0.4 installs update
into it). Signing, notarization, the DMG and the updater archive are unchanged.
Updates run through the core (`AppService.CheckForUpdates` / `ApplyUpdate`):
Wails' swap helper replaces the bundle after the core announces
`dockiva:quit-for-update` and the app quits.

## Status

Done:

- Overview, containers (live usage, ports, logs, a line-mode terminal, details:
  environment, mounts, networks, labels), Compose projects (up, build, rebuild,
  start, stop, restart, down, import by file, folder or drag and drop), images,
  volumes, networks.
- Engine settings (backend selection, external endpoint, start and stop),
  storage (disk usage, prune, engine data folders), migration from Docker
  Desktop or OrbStack.
- Updates (check on launch and daily, Check for Updates…, install and relaunch),
  open at login, start hidden in the menu bar, menu bar panel.

Not yet:

- A full terminal emulator (the terminal is line-oriented; SwiftTerm would add
  one but is a new dependency).
- With Dockiva Engine: disk usage (the engine has no `/system/df`), migration
  into it, and stopping or deleting it from the app.
