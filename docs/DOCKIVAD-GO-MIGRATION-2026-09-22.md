# Go dockivad migration — 2026-09-22

The supported Dockiva preview control plane now runs in Go. The packaged engine
has no CPython runtime and invokes no Python workers. This completes replacement
of the old preview daemon/supervisor path; it does not complete every production
control-plane contract or make the Linux runtime a general Docker replacement.

## Implementation

- `dockivad/api.go`: authenticated Docker API subset 1.24–1.45, image/container
  listing and inspection, create/start/stop/restart/remove, wait and framed
  snapshot logs. Unsupported options fail explicitly. Requests/body/headers and
  concurrent handlers are bounded. Tokens rotate on API restart.
- `dockivad/image.go`, `registry.go`: strict OCI JSON/digest/config validation,
  bounded layer expansion and private rootfs preparation, whiteouts, collision
  checks, atomic cache import and scoped registry pulls. Links, devices and path
  escapes are rejected. Registry requests refuse redirects/proxies; credentials
  stay on the registry origin, token requests omit them, and pulls have a total
  20-second network deadline. HTTP is test-only literal loopback opt-in.
- `dockivad/lifecycle.go`: one detached Go supervisor per running container,
  generation/status tracking, private logs and authenticated stop control.
  An inherited lock owns each run. Four concurrent runtimes and 1024 MiB reserved
  guest allocations are admitted per state store. Short-lived programs may exit
  while Stop/Restart runs; terminal status and lock cleanup resolve that race.
- `native/worker_launcher.c`: C guard applies the existing host rlimits before
  starting the native runtime. A pipe tracks supervisor liveness; supervisor death
  kills and reaps the native child. The supervisor also kills remaining group
  members when a guard exits. This is lifecycle ownership, not a sandbox claim.
- `dockivad/main.go`, `common.go`: private state, exclusive daemon ownership,
  safe file reads and atomic state records, Unix socket startup and recovery.
  Container creation publishes a complete directory with rename. Startup removes
  interrupted unpublished creation directories. Power-loss durability of arbitrary
  guest data and shared image-store reclamation remain separate storage gates.

Go validates and prepares images; C remains responsible for guest instructions,
Linux ABI/state and host adapters. The shipped shell/HTTP demos retain the
interpreter. The digest-pinned prepared Redis preset retains Unicorn. General
capability profiles and stock-image compatibility remain open.

The OCI validation working set is bounded: 32 layers, 32 MiB per blob, 128 MiB
packed image, 64 MiB expanded layers and 4096 members/paths. Temporary validation
allocations are garbage-collected and offered back to the OS after startup/image
operations. This is not an RSS ceiling. Per-store guest reservations also do not
account for the full process tree or enforce a machine-wide memory cap.

## State and upgrade behavior

The existing image-cache and container JSON formats are preserved. A running old
Python supervisor remains controllable from Go; its next start uses Go/C. The old
control client can inspect and stop a new Go supervisor for rollback. No user
rootfs rewrite or migration deletion is required.

Dockiva reuses an already running API. Opening a new app does not silently replace
an existing Python daemon. The separate Go test launcher uses
`~/.vmfree-dockiva-go-test`, so its tests exercise the new helper. Existing installed
apps and user containers were not replaced by this build.

On daemon exit/crash, supervisors and workloads continue. Restart rotates the API
token and discovers the same worker generations. On supervisor death, the guard
ends the workload; inspection marks the unowned run failed. A stale live PID is
never killed solely because it appears in an old status file. A legacy orphan
may require manual recovery before a new worker can start.

## Build and integration

```sh
python3 scripts/bundle_dockiva_engine.py --output build/dockiva-go
go -C dockivad test -race -ldflags=-linkmode=external ./...
python3 scripts/verify_dockivad.py \
  --helper build/dockiva-go/vmfree-engine/vmfree-engine \
  --report build/dockivad-recovery.json
python3 scripts/verify_dockiva_capacity.py \
  --helper build/dockiva-go/vmfree-engine/vmfree-engine \
  --report build/dockivad-capacity.json --sample-seconds 10
```

Python is development tooling only. Go uses the standard library with no external
modules. The local Go 1.22/macOS toolchain needs external linking and explicit
ad-hoc signing. Each executable is verified before bundle publication. `dockivad`
and the legacy helper filename `vmfree-engine` contain the same Go program and
are separately signed copies.

Dockiva packaging validates the five Go/C executable entries, rejects Python
files, and replaces its generated helper directory so obsolete CPython files
cannot survive a rebuild. The app metadata resolves its actual executable name.
The final app is `bin/dockiva-go-test.app` in the Dockiva checkout; double-click
`bin/Launch Dockiva Go Test.command` to use isolated engine state.

This app still uses the current Vue/Wails UI. The SwiftUI/AppKit migration and
Developer-ID/notarized clean-Mac distribution gate remain open. GUI button clicks,
older macOS releases and actual 8 GB hardware have not been qualified here.

## Validation of the final artifact

All checks below passed on Apple Silicon macOS 26.5.1, with 32 GiB host RAM:

| Check | Evidence |
| --- | --- |
| Eight Go test groups with race detector | [Go tests](../evidence/dockivad-go/go-tests.txt); OCI/JSON/path limits, fixtures, registry, API, stale state and interrupted creation |
| 21 migration/recovery checks | [Report](../evidence/dockivad-go/recovery.json), [output](../evidence/dockivad-go/recovery.txt); includes 20 immediate restarts then removal, concurrent admission, daemon/supervisor/guard crashes, token rotation, rollback control and Python-free process tree |
| 20 capacity/reconnect checks | [Report](../evidence/dockivad-go/capacity.json); 100 HTTP requests across four servers, fifth-run rejection, slot reuse and complete cleanup |
| All Dockiva Go packages with final embedded helper | [Output](../evidence/dockivad-go/dockiva-tests.txt); automatic startup/reuse, shell/HTTP and prepared Redis PING/SET/GET/AOF recovery |
| Separate live API adapter | [Output](../evidence/dockivad-go/live-adapter.txt); imported fixture lifecycle |
| Frontend/app build | [Output](../evidence/dockivad-go/app-build.txt); production frontend and Go desktop build |
| Packaging, signature, source/artifact identity | [Checks](../evidence/dockivad-go/packaging-checks.txt), [manifest](../evidence/dockivad-go/artifact.json); final embedded helper hash matches the recovery/capacity reports |

`go vet ./...` also passed in the daemon module. The test suites exercise both
old and new clients; Python appears only in the development harness and explicit
legacy-migration portion. After migration, four live containers plus their daemon,
supervisors and C guards/runtimes form a 13-process tree with no Python process.

Ten-second resource windows recorded 152.2 MiB summed RSS for the idle API
and 217.5 MiB for the API plus four HTTP workloads; CPU was
0.00% and 0.84% of one core respectively. GUI/WebViews are excluded.
These RSS values are higher than the historical Python-bundle measurements and
establish no memory improvement. Returning temporary allocations to the OS did
not reduce this RSS measure. Shared/reusable pages and physical footprint need
separate accounting; this remains an unresolved performance task, not an 8 GB
qualification or Docker comparison.

## Scope still open

Port publishing, bridge/DNS networking, general volumes/builds/Compose, streaming
logs/attach, rich versioned compatibility profiles, machine-wide resource pressure
and JIT accounting are not completed by this migration. Neither production
confinement nor Valkey V11/PostgreSQL P12 is accepted. Prepared Redis tests protect
the existing preview behavior only. No Docker Desktop RAM/CPU/battery advantage
is established.
