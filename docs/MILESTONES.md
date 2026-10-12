# Milestone notes

Implementation decisions and known limitations of each app milestone, oldest first,
merged from the former root `README-MILESTONE*.md` files and `ARCHITECTURE.md`. The
`dockiva-milestoneN/apply.sh` steps refer to the patch bundles each milestone was delivered as.

- [Dockiva — Milestone 9: Native-per-platform engine architecture](#m9)
- [Dockiva — Milestone 10](#m10)
- [Dockiva — Milestone 11: Direct containerd on Linux](#m11)
- [Dockiva — Milestone 12: CNI networking + localhost ports](#m12)
- [Dockiva — Milestone 13: Native macOS Virtualization.framework backend](#m13)
- [Dockiva — Milestone 14: Advanced tray / menu-bar controls](#m14)
- [Dockiva — Milestone 15: Grouped tray containers](#m15)
- [Dockiva — Milestone 16 Combined Runtime Release](#m16)
- [Dockiva — Milestone 16.1: Engine Auto-Recovery](#m16-1)
- [Dockiva — Milestone 16.1.2: Main App Container Group Fix](#m16-1-2)
- [Dockiva — Milestone 16.1.3: Docker Desktop Start UX Fix](#m16-1-3)
- [Milestone 16.1.3 — Linux Docker Desktop Lifecycle](#m16-1-3-lifecycle)

<a id="m9"></a>

## Dockiva — Milestone 9: Native-per-platform engine architecture

This milestone is an architectural refactor for the lightweight goal:

> One shared Wails/Vue application and Go core, with an engine backend compiled
> specifically for Linux, macOS, or Windows.

### Main change

The old:

```text
docker_engine.go
    runtime.GOOS switches
    Linux code
    macOS code
    Windows code
```

is replaced by:

```text
engine_common.go
engine_linux.go
engine_darwin.go
engine_windows.go
```

using Go build constraints.

#### Linux

```go
//go:build linux
```

Only the native Linux backend is compiled.

#### macOS

```go
//go:build darwin
```

Only the macOS backend is compiled.

The current implementation still uses Lima as a working transitional runtime.
The important Milestone 9 change is that it is now isolated behind the macOS
backend boundary.

#### Windows

```go
//go:build windows
```

Only the WSL2 implementation is compiled.

### Why do this before adding more features?

It gives us a clean path toward:

```text
Linux   → direct containerd
macOS   → Apple Virtualization.framework + minimal guest
Windows → Dockiva-owned minimal WSL2 rootfs
```

without maintaining three complete UI/application implementations.

See:

```text
ARCHITECTURE.md
```

for the full design.

### Existing features remain

Milestone 9 is cumulative from Milestone 8:

- Containers
- Compose grouping
- Compose up/down/build/rebuild
- live Docker events
- live stats
- live logs
- xterm terminal
- details/env/mounts
- images
- volumes
- networks
- storage/prune
- Compose import/drop
- system tray
- autostart
- persistent settings
- macOS Lima managed engine
- Windows WSL2 managed engine
- Linux native engine
- Windows/macOS/Linux build scripts

### Apply

```bash
chmod +x dockiva-milestone9/apply.sh

./dockiva-milestone9/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then on your Linux machine:

```bash
cd /home/darith/mbanq/dockiva/dockiva

go mod tidy

wails3 build -tags gtk3

./bin/dockiva
```

### Important upgrade behavior

The apply script backs up and removes the old:

```text
docker_engine.go
```

If it remains in the project, its methods would conflict with the new shared
engine API.

### Cross-platform builds

Windows:

```bash
./scripts/build-windows.sh
```

macOS:

```bash
./scripts/build-macos.sh
```

All:

```bash
./scripts/build-all-from-linux.sh
```

### Next recommended milestone

Milestone 10:

1. Add `ContainerRuntime` interface.
2. Keep Docker/Moby as the first runtime implementation.
3. Add direct containerd implementation for Linux.
4. Stop making the application core depend directly on Docker Engine semantics.
5. Prepare the same interface for the future minimal macOS/Windows guests.

That is the next major step toward an actually lightweight runtime rather than
only a lightweight desktop frontend.

<a id="m10"></a>

## Dockiva — Milestone 10

Milestone 10 introduces the runtime abstraction required for the lightweight
architecture.

### New

- `ContainerRuntime` Go interface
- Docker/Moby runtime adapter
- Container lifecycle delegated through the runtime
- Linux-only containerd adapter skeleton
- containerd socket and `ctr` detection
- Runtime status/capabilities on the Engine screen
- Build-tag separation for non-Linux containerd detection

### Important

Docker/Moby remains the active runtime.

containerd can be detected on Linux but is **not selectable yet**. This is
intentional: we do not want to regress Compose, ports, logs, exec, or networking
while direct containerd behavior is incomplete.

### Apply

```bash
chmod +x dockiva-milestone10/apply.sh

./dockiva-milestone10/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then:

```bash
cd /home/darith/mbanq/dockiva/dockiva
go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

Verify:

```bash
./scripts/verify-runtime-architecture.sh
```

### Next milestone

Milestone 11 will implement the first real direct-containerd lifecycle on Linux
inside a dedicated `dockiva` namespace.

<a id="m11"></a>

## Dockiva — Milestone 11: Direct containerd on Linux

Milestone 11 makes the Linux containerd adapter genuinely usable.

### Runtime support

#### Docker / Moby

Still the full-feature runtime:

- containers
- lifecycle
- Compose
- ports
- images
- volumes
- networks
- stats
- logs
- terminal
- inspect

#### containerd (Linux, experimental)

Now selectable when Dockiva can access a containerd socket.

Implemented:

- dedicated `dockiva` namespace
- direct containerd v2 client
- image pull + unpack
- snapshotter selection
- create OCI container
- create/start task
- list container/task state
- stop with SIGTERM then SIGKILL timeout
- restart
- delete task/container
- snapshot cleanup
- persistent runtime choice

Not implemented yet:

- CNI networking
- host port publishing
- live logs
- exec terminal
- stats UI
- Compose
- Docker-style volumes/networks

Those features are hidden/disabled when containerd is active.

### containerd version

The patch adds:

```bash
go get github.com/containerd/containerd/v2@v2.3.4
```

Milestone 11 targets the current v2 client API.

### Rootless first

Dockiva prefers an accessible rootless containerd socket when present.

It also checks standard rootful sockets:

```text
/run/containerd/containerd.sock
/var/run/containerd/containerd.sock
```

Dockiva should not be run as root just to manage containers.

If a system socket exists but your normal user cannot connect, containerd will
appear unavailable in the Engine screen.

### Apply

```bash
chmod +x dockiva-milestone11/apply.sh

./dockiva-milestone11/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then:

```bash
cd /home/darith/mbanq/dockiva/dockiva

go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

### Try containerd

Open:

```text
Engine
  → Container Runtime
  → containerd (direct)
  → Use Runtime
```

When containerd is active, Containers gets a new Direct containerd panel.

Example:

```text
Image: alpine:latest
Name: test-alpine
Command: sleep 3600
Start immediately: yes
```

Click:

```text
Pull Image
Create
```

Then Start / Stop / Restart / Delete use containerd directly.

### Namespace isolation

Dockiva uses only:

```text
containerd namespace: dockiva
```

It will not list or manage Kubernetes `k8s.io` containers or Docker's internal
containerd namespaces.

### Snapshotter

Dockiva tries:

1. `DOCKIVA_CONTAINERD_SNAPSHOTTER`, when set
2. `overlayfs`
3. `native`

The first snapshotter reported as supported by containerd is used.

### Important networking limitation

Milestone 11 does not configure CNI.

Containers created through the direct containerd runtime should be treated as
local process-isolation experiments for now. Do not expect Docker-style bridge
networking or `-p host:container` behavior.

### Verify

```bash
./scripts/check-containerd-runtime.sh
```

The helper shows:

- containerd sockets
- `ctr`
- namespaces when permission allows
- Dockiva namespace

### Next milestone

Milestone 12 should add CNI networking and port publishing:

```text
Dockiva containerd
   ↓
CNI bridge
   ├── container IP
   ├── DNS
   └── host port mappings
```

After that, logs/exec/stats can be migrated.

<a id="m12"></a>

## Dockiva — Milestone 12: CNI networking + localhost ports

Milestone 12 makes direct containerd practical for local web/API development.

### New for Linux direct containerd

- persistent network namespaces
- CNI bridge network
- host-local IPv4 allocation
- outbound masquerading
- container IP display
- host DNS resolver inside containers
- localhost-only TCP port publishing
- port collision checks
- network teardown when the container is deleted
- network state persists across task stop/start/restart

### Network

```text
name:    dockiva-net
bridge:  dockiva0
subnet:  10.89.0.0/16
gateway: 10.89.0.1
```

The CNI chain uses loopback + bridge/host-local + portmap.

### Privilege design

The Wails app remains unprivileged.

A small root helper (`dockiva-netd`) owns the Linux operations that require
network privileges and exposes only a narrow Unix-socket API at:

```text
/run/dockiva/netd.sock
```

It accepts Dockiva container IDs and localhost TCP mappings only.

### Apply

```bash
chmod +x dockiva-milestone12/apply.sh

./dockiva-milestone12/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

### One-time networking install

```bash
cd /home/darith/mbanq/dockiva/dockiva
./scripts/install-containerd-networking.sh
```

The installer may use sudo to install CNI plugins/iproute2, create the
`dockiva` group, install the helper, write the fixed CNI config, and start the
systemd service.

Then:

```bash
./scripts/check-containerd-networking.sh
```

Log out/in after first install for permanent `dockiva` group membership.

### Build

```bash
go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

### Test

Select:

```text
Engine → Container Runtime → containerd (direct)
```

Then:

```text
Image:   nginx:latest
Name:    test-web
Ports:   8080:80
```

Pull Image, then Create.

The container should get a `10.89.x.x` address and the UI should show:

```text
8080:80/tcp
```

Then open:

```text
http://localhost:8080
```

Multiple mappings:

```text
8080:80,8443:443
```

### Safety restrictions

Milestone 12 direct-containerd port publishing is fixed to:

- host IP `127.0.0.1`
- TCP
- host ports `1024-65535`

Rootless containerd remains supported for containers, but this host CNI bridge
integration is intentionally disabled for rootless containerd.

### DNS

Networked containers use containerd's `oci.WithHostResolvconf`, which mounts
the host resolver configuration read-only. This provides external DNS but not
Docker-style service-name discovery.

### Still pending for direct containerd

- live logs
- xterm exec
- CPU/RAM stats
- service-name DNS
- user-defined CNI networks
- UDP/LAN publishing
- Compose

### Next

Milestone 13: direct-containerd logs, exec terminal, and task metrics.

<a id="m13"></a>

## Dockiva — Milestone 13: Native macOS Virtualization.framework backend

Milestone 13 replaces the transitional Lima backend with a native macOS VM
backend built directly on Apple's `Virtualization.framework`.

### Target architecture

```text
Dockiva.app
   │
   ├── shared Go / Vue UI
   │
   ├── engine_darwin.go
   │
   └── dockiva-vmm (Swift)
          │
          ▼
   Virtualization.framework
          │
          ▼
   one minimal Linux VM
          │
          ├── containerd
          ├── runc
          └── dockiva-guestd
                 │
                 │ virtio-vsock
                 ▼
      macOS local Unix proxy
                 │
                 ▼
       Go containerd client
```

No Lima is used by the native backend.

No Docker Desktop is required by the native backend.

### Why a Swift helper?

`Virtualization.framework` is an Apple native Objective-C/Swift framework and
requires the `com.apple.security.virtualization` entitlement.

Instead of forcing the whole Wails/Go desktop app to own VM lifecycle details,
Milestone 13 isolates the native VMM in:

```text
native/macos/DockivaVMM
```

The Go macOS backend starts/stops that helper and connects to the guest
containerd service through a local Unix socket.

### Host ↔ guest transport

The VM has a `VZVirtioSocketDeviceConfiguration`.

Inside Linux:

```text
dockiva-guestd
  vsock port 10250
      ↓
/run/containerd/containerd.sock
```

On macOS, `dockiva-vmm` exposes:

```text
~/Library/Application Support/Dockiva/run/containerd.sock
```

Every local Unix-socket connection is relayed to guest vsock port `10250`.

That lets the existing Go containerd client talk to the guest without exposing
containerd over TCP.

### VM resources

Defaults:

```text
CPU: 4
RAM: 2048 MiB
disk: 2 GiB sparse ext4 image
```

Override the Go backend defaults before launching Dockiva:

```bash
export DOCKIVA_VM_CPUS=2
export DOCKIVA_VM_MEMORY_MIB=1024
```

For a small development workload, 1–2 GiB is a reasonable starting point.

### VM guest

The provided guest asset builder creates a minimal ARM64 Alpine root filesystem
containing:

- BusyBox/Alpine base
- containerd
- containerd-shim-runc-v2
- runc
- CNI binaries
- dockiva-guestd
- custom `/sbin/dockiva-init`

There is no desktop environment and no Docker daemon.

### Current pinned guest components

The asset builder currently pins:

```text
Alpine Linux 3.24.1
containerd 2.3.1
runc 1.5.1
CNI plugins 1.9.1
```

The kernel fetch follows the current Apple-container-style optimized Kata kernel
layout and produces the file Dockiva expects as `vmlinux`.

### Step 1 — apply Milestone 13

```bash
chmod +x dockiva-milestone13/apply.sh

./dockiva-milestone13/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

### Step 2 — build guest assets on Linux

On your Linux machine:

```bash
cd /home/darith/mbanq/dockiva/dockiva

./scripts/build-macos-guest-assets-linux.sh
```

This produces:

```text
dist/macos-guest/
├── vmlinux
├── rootfs.ext4
└── manifest.txt
```

Copy that folder to your Mac.

### Step 3 — build the self-contained macOS app

On the Mac:

```bash
./scripts/build-macos.sh arm64 --guest-assets /path/to/macos-guest
```

Use `amd64` for an Intel Mac. Add `--install` to copy the completed app to
`~/Applications/Dockiva.app`.

The build script packages:

```text
Dockiva.app/Contents/Resources/dockiva-vmm
Dockiva.app/Contents/Resources/guest/vmlinux
Dockiva.app/Contents/Resources/guest/rootfs.ext4
```

The helper is ad-hoc signed during development with:

```text
com.apple.security.virtualization = true
```

For a production release, sign the embedded helper with your Developer ID
identity as part of the final app signing pipeline.

### Step 4 — run Dockiva

```bash
open dist/macos-arm64/Dockiva.app
```

On the first native-engine start, Dockiva copies the bundled guest assets to
the writable runtime location:

```text
~/Library/Application Support/Dockiva/guest/
├── vmlinux
└── rootfs.ext4
```

Then select:

```text
Engine
  → Native Dockiva VM
  → Start / Connect
```

The backend starts `dockiva-vmm`.

When the guest's vsock proxy is ready, Dockiva automatically selects:

```text
containerd (native macOS guest)
```

### macOS direct-containerd capabilities in Milestone 13

Implemented:

- native VM start/stop
- no Lima dependency
- no Docker Desktop dependency
- containerd over virtio-vsock
- dedicated `dockiva` namespace
- OCI image pull/unpack
- container create/start/stop/restart/delete

Not yet implemented on the macOS guest:

- CNI host-port forwarding to macOS
- container IP UI
- logs
- xterm exec
- CPU/RAM stats
- shared folders
- dynamic memory balloon control
- VM snapshots

Those should follow after validating this native transport on Apple Silicon.

### Linux and Windows

Linux direct-containerd from Milestones 11/12 is unchanged.

Windows remains on the WSL2 backend for now.

### Development note

Milestone 13's Swift helper must be compiled/tested on macOS because
`Virtualization.framework` is not available to the Linux build host.

The package includes static validation and build scripts, but the actual native
VM boot path needs to be exercised on your Mac.

### Check native backend

```bash
./scripts/check-macos-native-engine.sh
```

### Next milestone

Milestone 14 should bring macOS guest parity with Linux direct-containerd:

1. guest CNI networking
2. macOS localhost port forwarding
3. shared project folders via VirtioFS
4. VM CPU/RAM settings in UI
5. guest health/boot timing

<a id="m14"></a>

## Dockiva — Milestone 14: Advanced tray / menu-bar controls

Milestone 14 turns the system tray / macOS menu-bar item into a real Dockiva
control center.

### What it adds

The tray menu is now dynamic and refreshes every 5 seconds.

Example:

```text
Engine: Running · containerd
Containers: 4 running · 1 stopped
──────────────────────────────
Engine
  Engine: Running · containerd
  ────────────────────────────
  Stop Engine
  Restart Engine

Containers (5)
  Start All Stopped (1)
  Stop All Running (4)
  ────────────────────────────
  ● api
      running
      ───────
      Stop
      Restart

  ● postgres
      running
      ───────
      Stop
      Restart

  ○ worker
      exited
      ───────
      Start

──────────────────────────────
Show/Hide Dockiva
Refresh
✓ Start at Login
──────────────────────────────
Quit Dockiva
```

### Engine controls

From the menu bar / system tray:

- Start / Connect Engine
- Stop Engine
- Restart Engine
- Reconnect external engine

Managed engine stop/restart is only shown where Dockiva actually owns the
engine:

```text
macOS   → native Virtualization.framework VM
Windows → WSL2 managed engine
```

For Linux host Docker/containerd and explicitly external Docker endpoints,
Dockiva does not attempt to stop the host service from the tray.

### Container controls

The Containers submenu supports:

- Start All Stopped
- Stop All Running
- Start one stopped container
- Stop one running container
- Restart one running container

This works through the active `ContainerRuntime`, so the same tray UI can
control Docker/Moby containers or direct-containerd containers.

To keep the native menu manageable, Dockiva shows up to 8 containers directly.
If more exist, the menu offers:

```text
Open Dockiva to view N more…
```

### Live state

The tray rebuilds itself every 5 seconds using Wails' supported dynamic menu
rebuild pattern.

After a tray action, Dockiva also emits:

```text
dockiva:tray-refresh
```

The Vue UI listens for this event and refreshes the currently visible screen.

### Busy/error feedback

Long operations do not block the tray callback.

While an operation runs:

```text
Working: Stopping engine…
```

If an operation fails:

```text
Last error: ...
```

appears in the next menu state.

### Start at Login

The tray now contains a native checkbox:

```text
✓ Start at Login
```

It uses the existing cross-platform Wails autostart integration.

### Apply

```bash
chmod +x dockiva-milestone14/apply.sh

./dockiva-milestone14/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then on your current Linux machine:

```bash
cd /home/darith/mbanq/dockiva/dockiva

go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

### macOS

This is exactly the menu shown from the Dockiva status item in the macOS menu
bar.

The native macOS engine work from Milestone 13 is unchanged.

### Windows/Linux

The same controller uses Wails' system tray API, so Windows notification-area
and supported Linux desktop tray menus receive the same controls.

Linux tray availability still depends on the desktop environment/system-tray
support.

<a id="m15"></a>

## Dockiva — Milestone 15: Grouped tray containers

Milestone 15 changes the tray from a flat container list to the same grouping
model used by the main Containers screen.

### Tray structure

Example:

```text
Engine: Running · docker
Containers: 8 running · 2 stopped
────────────────────────────────

Engine
  ...

Containers (10)
  Start All Stopped (2)
  Stop All Running (8)
  Restart All Running (8)

  school-system (5)
    4 running · 1 stopped · Compose project
    ─────────────────────────────
    Start All Stopped (1)
    Stop All Running (4)
    Restart Running (4)
    ─────────────────────────────
    ● api
       running
       Stop
       Restart
    ● postgres
       running
       Stop
       Restart
    ○ worker
       exited
       Start

  poker-game (3)
    3 running · 0 stopped · Compose project
    ...

  Standalone Containers (2)
    1 running · 1 stopped · standalone
    ─────────────────────────────
    Start All Stopped (1)
    Stop All Running (1)
    Restart Running (1)
    ─────────────────────────────
    ● nginx-test
       running
       Stop
       Restart
    ○ redis-test
       exited
       Start
```

### Grouping rules

Containers with Docker Compose label:

```text
com.docker.compose.project
```

are grouped by project name.

Containers without a Compose project are grouped under:

```text
Standalone Containers
```

That includes ordinary `docker run` containers and direct-containerd resources
that do not belong to a Compose project.

### Group actions

Every group supports:

- Start All Stopped
- Stop All Running
- Restart Running

The top Containers menu also retains global:

- Start All Stopped
- Stop All Running
- Restart All Running

### Individual containers

Each individual container submenu shows:

- status
- published ports/localhost URL when present
- Start, or
- Stop + Restart

### Limits

To keep native menus usable:

```text
max project/standalone groups shown: 10
max containers shown per group:      8
```

When a group has more containers, the tray offers:

```text
Open Dockiva to view N more…
```

The full Containers page remains the place for large environments.

### Apply

```bash
chmod +x dockiva-milestone15/apply.sh

./dockiva-milestone15/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then:

```bash
cd /home/darith/mbanq/dockiva/dockiva

go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

<a id="m16"></a>

## Dockiva — Milestone 16 Combined Runtime Release

This cumulative package combines the planned Milestones 16, 17, and 18 work
into one development release.

### 16A — macOS native engine usability

The native `Virtualization.framework` backend from Milestone 13 is preserved.

Added:

- VirtioFS sharing for `~/Projects`
- guest mount point `/mnt/dockiva/host`
- existing minimal Linux + containerd design remains
- no Lima requirement for native mode
- no Docker Desktop requirement for native mode

The macOS VMM still needs a real compile/boot validation on macOS because
`Virtualization.framework` is unavailable on the Linux build host.

### 16B — direct containerd developer tools

Linux direct-containerd now integrates with the existing Dockiva UI APIs for:

- live stdout/stderr logs for newly started direct-containerd tasks
- interactive xterm `/bin/sh`
- terminal input and resize
- CPU usage from cgroup v2
- memory usage / limit
- PID count

The existing Docker/Moby implementations remain unchanged.

Important: containers whose tasks were already running before this milestone
may not have Dockiva-managed log files. Restart them once after applying the
milestone to enable the new log capture path.

### 16C — dedicated Windows WSL2 foundation

The package adds a Dockiva-owned WSL2 rootfs workflow so future Windows
containerd support does not need to modify a user's Ubuntu/Debian distro.

Build the rootfs on Linux:

```bash
./scripts/build-windows-wsl-rootfs-linux.sh
```

This creates:

```text
dist/windows-wsl/dockiva-wsl-rootfs.tar
```

Install it on Windows:

```powershell
.\scripts\install-dockiva-wsl.ps1 `
  -Rootfs C:\path\dockiva-wsl-rootfs.tar
```

The dedicated distro name is:

```text
Dockiva
```

This Windows path is still experimental and needs a real WSL2 target test.

### Tray fixes retained

Milestone 16 is based on the latest Milestone 15 tray-fixed package:

- clicking the tray icon does not open/toggle Dockiva
- `Show Dockiva` is explicit
- Compose projects are grouped in the tray
- non-Compose containers are under `Standalone Containers`
- tray refresh is event-driven rather than every 5 seconds

### Apply

```bash
chmod +x dockiva-milestone16/apply.sh

./dockiva-milestone16/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then:

```bash
cd /home/darith/mbanq/dockiva/dockiva

go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

### Validation performed on the package

- all Go source files passed `gofmt` parsing
- all shell scripts passed `bash -n`
- the previous Vue malformed `filteredGroups` quote is not present
- the latest event-driven/grouped tray implementation is retained

The macOS Swift helper and Windows WSL2 provisioning must still be tested on
their real target operating systems.

<a id="m16-1"></a>

## Dockiva — Milestone 16.1: Engine Auto-Recovery

Milestone 16.1 fixes offline/stale Docker endpoint handling.

A common example is Linux keeping this Docker Desktop context after Desktop is
stopped or removed:

```text
unix:///home/user/.docker/desktop/docker.sock
```

Dockiva no longer treats that context as the only possible endpoint.

### Endpoint discovery

`Reconnect` checks reachable local endpoints in priority order, including:

Linux:

```text
DOCKER_HOST
active Docker CLI context
/var/run/docker.sock
/run/docker.sock
$XDG_RUNTIME_DIR/docker.sock
~/.docker/run/docker.sock
~/.docker/desktop/docker.sock
```

macOS also checks the normal Docker and Colima sockets.

Windows checks the standard Docker named pipe.

A candidate is selected only after a real Docker API ping succeeds.

### Start Engine on Linux

When the user explicitly clicks `Start Engine` Dockiva does:

```text
1. Reconnect/discover an already-running Docker Engine
2. Try rootless Docker:
   systemctl --user start docker.service
3. Try system Docker:
   systemctl start docker.service
4. If administrator authorization is required and pkexec exists:
   pkexec systemctl start docker.service
5. Discover/reconnect the Docker API again
```

Dockiva never runs `sudo` because a desktop app should not request a password
through a hidden terminal.

### Automatic recovery

Two settings are available:

```text
Auto reconnect engine
Start engine when Dockiva starts
```

`Auto reconnect engine` defaults on.

Automatic Linux startup is deliberately non-privileged: it may start a
rootless user Docker service, but it will not show a surprise administrator
prompt.

macOS/Windows may start the Dockiva-owned VZ/WSL2 backend when configured.

External Docker Desktop is never launched automatically.

### UI

When the engine is offline:

```text
[ Reconnect ] [ Start Engine ]
```

`Reconnect` only discovers/reconnects an existing Docker daemon.

`Start Engine` is the explicit operation allowed to start the Linux service.

### Apply

```bash
chmod +x dockiva-milestone16.1/apply.sh

./dockiva-milestone16.1/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then:

```bash
cd /home/darith/mbanq/dockiva/dockiva

go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

<a id="m16-1-2"></a>

## Dockiva — Milestone 16.1.2: Main App Container Group Fix

This fixes the issue where many Compose groups appeared to disappear in the
main Dockiva Containers page.

### Root cause

The frontend was not limiting the groups. It already rendered every group from
`ListContainers()`.

The problem was engine auto-recovery.

On Linux these are different Docker daemons:

```text
Docker Desktop
  ~/.docker/desktop/docker.sock

System Docker Engine
  /var/run/docker.sock
```

They have different images, containers, and Compose projects.

Milestone 16.1 could see Docker Desktop offline and silently reconnect Dockiva
to System Docker. The API became healthy, but the main Containers page was then
showing the system daemon's smaller/different container store.

### Fix

Dockiva now remembers its configured Docker endpoint and preserves that engine
identity.

If Docker Desktop was selected:

```text
Reconnect
  -> same Docker Desktop socket only

Start Engine
  -> systemctl --user start docker-desktop
  -> wait for the same Docker Desktop socket
```

It will not silently jump to `/var/run/docker.sock`.

If System Docker is selected, Start Engine starts `docker.service`.

If rootless Docker is selected, Start Engine starts the user's rootless
`docker.service`.

### Engine page

The Engine page now shows the actual selected engine:

```text
Docker Desktop · unix:///home/user/.docker/desktop/docker.sock
```

or:

```text
System Docker Engine · unix:///var/run/docker.sock
```

This makes it clear which daemon owns the containers shown in the main app.

### Apply

```bash
chmod +x dockiva-milestone16.1.2/apply.sh

./dockiva-milestone16.1.2/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then:

```bash
cd /home/darith/mbanq/dockiva/dockiva
go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

After starting Dockiva, open Engine and verify it says `Docker Desktop` if
those are the containers/projects you expect.

<a id="m16-1-3"></a>

## Dockiva — Milestone 16.1.3: Docker Desktop Start UX Fix

This fixes two issues visible when Docker Desktop is installed but stopped.

### Fixed: installed vs running

Previously:

```text
Docker Desktop stopped
→ Engine: not provisioned
```

That was incorrect because `EngineInstalled` was derived from Docker API
reachability.

Milestone 16.1.3 checks the Linux systemd unit independently:

```text
systemctl --user show docker-desktop.service \
  --property=LoadState --value
```

So a stopped but installed Docker Desktop now shows:

```text
Engine stopped / unavailable
Engine: installed
Docker Desktop is stopped.
```

### Fixed: long Starting… state

Docker Desktop is now launched with:

```bash
systemctl --user start --no-block docker-desktop
```

The command returns immediately, then Dockiva polls only the configured Docker
Desktop socket for up to ~20 seconds.

It does not fall back to `/var/run/docker.sock`.

### Apply

```bash
chmod +x dockiva-milestone16.1.3/apply.sh

./dockiva-milestone16.1.3/apply.sh \
  /home/darith/mbanq/dockiva/dockiva
```

Then:

```bash
cd /home/darith/mbanq/dockiva/dockiva
go mod tidy
wails3 build -tags gtk3
./bin/dockiva
```

If Docker Desktop still does not become ready, Dockiva returns an error instead
of staying in an indefinite-looking `Starting…` state.

<a id="m16-1-3-lifecycle"></a>

## Milestone 16.1.3 — Linux Docker Desktop Lifecycle

Installed state and running state are now independent:

```text
systemd user unit loaded?
       │
       └── EngineInstalled

Docker API socket responds?
       │
       └── Running
```

Start is non-blocking:

```text
Start Engine
    │
    ├─ systemctl --user start --no-block docker-desktop
    │
    └─ poll configured Desktop socket (~20 sec max)
```

The selected Docker engine identity from Milestone 16.1.2 remains preserved.
