# VM-free default engine on Apple Silicon

Production architecture is frozen in [baseline v1.0](docs/FINAL-ARCHITECTURE-V1.md). SwiftUI/AppKit and Go/C execution without Python are required migrations; this page describes the current Vue/Wails and bundled-Python preview. The runtime remains experimental. Valkey V0–V11 is the first major compatibility gate, PostgreSQL P0–P12 the second; both remain unaccepted. Existing Redis tests remain regression protection. Runtime semantics must use generic Linux state objects without named-application compatibility branches.

## Current state (2026-10-12)

This section supersedes older capability statements further down the page.

- **Execution backends.** New containers from unpinned images run **natively** under
  Hypervisor.framework (`vmfree-c-hvf`): the CPU executes guest ARM64 code directly,
  with no guest kernel or Linux VM; every Linux system call is served by the
  runtime. Where a VM cannot be created (inside VMs, CI runners, missing
  entitlement), the engine falls back to the interpreter. The Unicorn backend has
  been removed. Inspect shows the choice in `DockivaCompatibility.Backend`.
- **Images.** `linux/arm64` images can be pulled from registries (including Docker
  Hub). Symlinks, hardlinks, `#!` entrypoints, any `WorkingDir` and declared volumes
  (as plain folders) are supported; images that set `User` are rejected.
- **Networking.** Containers share the Mac's network: outbound IPv4 TCP and UDP
  reach the internet, and DNS uses the Mac's IPv4 nameservers (written to
  `/etc/resolv.conf` at each start, with `/etc/hosts` mapping the container name
  and `host.docker.internal` to the shared loopback). Servers listening on
  `0.0.0.0` stay on loopback; `NetworkMode: none` keeps a container on loopback.
  Unix-domain sockets (stream and datagram, `socketpair`) live as socket files
  in the container's root; passing descriptors (`SCM_RIGHTS`) and Linux
  abstract names are not supported yet. No IPv6 or raw sockets.
- **Containers.** Private writable root by default; TCP port publishing on
  `127.0.0.1` (`HostPort:ContainerPort`); `/dev/null`, `/dev/zero`, `/dev/urandom`;
  process trees with fork/exec, threads (native backend), epoll (including
  edge-triggered), user switching (`setpriv`, `su-exec`).
- **Verified stock images:** `alpine`, `busybox` (musl and glibc) and
  `redis:7-alpine` with its normal entrypoint, `BGSAVE` and `redis-benchmark`.
  On the native backend, Redis 7.4.11 passes the official unit test suite as an
  external server on par with Redis in a Linux VM (2027 vs 2026 passing tests).
  `postgres:16-alpine` runs with its normal entrypoint (initdb, password setup,
  restart) and serves `psql` and `pgbench` clients; `docker stop` shuts it down
  cleanly and data survives restarts.
- **Processes.** Signal handlers, `sigaltstack`, per-thread signal masks and
  thread-directed signals; `docker stop` sends the image's `StopSignal` to PID 1
  and kills after 10 s. Shared memory (`MAP_SHARED` anonymous and System V),
  cross-process futexes, `signalfd`, `setsid`, `/proc/self/exe`, `/dev/fd/N`.
  Descriptors take the lowest free number; unknown system calls return `ENOSYS`.
- **Limits:** 64 live processes per container and 128 threads per process; up to
  1024 sockets, 1024 files and 509 pipe/epoll descriptors per process (guests see
  `RLIMIT_NOFILE` 4096), 64 pipes and 128 `signalfd`s per container, 4096 host
  descriptors per container; 8 MiB stacks; 512 MiB memory budget per
  container, charged as pages are first written (2 GiB of address space; `munmap`
  and `MADV_DONTNEED` return memory); 8 running containers. Unpublished ports
  remain reachable on the Mac's loopback. Files are capped at 4 GiB each; the
  Mac's disk is the only storage limit. Images up to 512 MiB packed, 1 GiB
  unpacked.
- **Performance.** A container gets one vCPU per Mac CPU (`--cpus`, set by the
  engine). Processes and the threads of one process run in parallel: every
  thread is scheduled on its own, sharing its process's page tables, and the
  process's own runtime work (memory changes, descriptors) is serialized per
  process rather than globally. Guests see the vCPU count through
  `sched_getaffinity`, so Go sets `GOMAXPROCS` to it. A CPU-bound Go program
  (12 goroutines) takes 1.10 s on one vCPU, 0.18 s on 8 and 0.16 s on 12;
  the same binary takes 0.17 s in an OrbStack VM. `vfork` from a process with
  several threads gives the child a private copy of memory (other threads keep
  running), so an exec failure reported through shared memory (glibc
  `posix_spawn`) is not seen by the parent. Hot system calls (socket and file
  reads and writes, `lseek`, `epoll_wait`, `mmap`/`munmap`, copy-on-write
  faults) run without the runtime's global lock. When such a call would block
  on sockets while other vCPUs are idle, the process waits for them itself for
  up to 2 ms, still without the lock, so a server's next request skips the
  scheduler. Process switches only reload the page-table base and
  descriptor-table pointers, and blocked processes wake on their own sockets,
  pipes, futexes and signals. `pgbench` against `postgres:16-alpine` (scale 1,
  12 vCPUs) compared with the same image in an OrbStack VM on the same Mac:

  | Workload | Native | VM |
  |---|---|---|
  | TPC-B, 1 client | 2,313 tps | 822 tps |
  | TPC-B, 10 clients | 3,467 tps | 1,065 tps |
  | TPC-B, 50 clients | 2,158 tps | 627 tps |
  | Select-only, 10 clients | 60,673 tps | 44,607 tps |
  | Select-only, 50 clients | 60,720 tps | 68,085 tps |
  | New connection per transaction | 286 tps | 347 tps |

  The write-heavy lead is not a durability-equivalent comparison: guest `fsync`
  maps to macOS `fsync`, which does not force a drive-cache flush
  (`F_FULLFSYNC`), and the VM's disk stack may flush differently. Connection
  setup (a fork per client) costs more than in a VM: page tables after a fork
  are updated region by region rather than rebuilt, but fork, exit and file
  opens still take the runtime's global lock.
- **Memory snapshots.** With several vCPUs touching pages at once, a write to
  the source of a Mach copy-on-write snapshot can show through in the
  snapshot (reproducible with Hypervisor.framework and `mach_vm_remap` alone,
  without the runtime). The runtime therefore never writes a snapshot's source
  again: every holder copies it before its first write. `fork_churn_guest`
  covers this with eight vCPUs.
- **Clocks.** The native backend maps a small vDSO, so musl and glibc read
  `CLOCK_REALTIME`/`CLOCK_MONOTONIC` and `gettimeofday` without a system call.
  Guest monotonic time excludes host sleep, as on Linux.

New Dockiva installations on Apple Silicon Macs default to **VM-free —
Experimental**. Opening Dockiva starts its bundled engine when “Start engine on
launch” is enabled (the default). Selecting VM-free in Engine Settings also starts
it. Saved engine choices and a disabled startup preference are preserved. Intel
Macs retain the Linux VM default; Windows and Linux defaults are unchanged.

The complete app includes Go dockivad (API, OCI preparation and supervisors),
C worker guards, both native ARM64 execution backends and three prepared demos.
It contains no CPython runtime and launches no Python workers. Users need no
terminal commands or runtime checkout. A running engine is
reused. Engine startup failure stays on the VM-free backend and shows an error;
there is no automatic fallback to Docker or a Linux VM.

Create a container using `vmfree/shell:demo` with command JSON
`["-c", "busybox echo hello"]`, leaving ports empty. The UI supports read-only
creation, list/inspect/start/stop/restart/remove and stdout/stderr snapshots.
Reopen logs to refresh. The API returns stdout then stderr; cross-stream ordering
is not promised. Engine and container supervisors continue when the UI closes,
so reopening reconnects to the same store. The API is not currently stopped by
Dockiva's managed-VM Stop button.

Default state lives in `~/.vmfree`; `VMFREE_HOME` overrides it for developer tests.
Dockiva authenticates over a private Unix socket and reloads its token per request.
Startup diagnostics are in `~/.vmfree/dockiva-engine.log`. A missing bundled helper
is a packaging error, not a request for the user to install Python.

## Project scope

Dockiva is the container workspace. vmfree-runtime is its separate experimental
ARM64 Linux user-mode compatibility engine, executing bounded programs on macOS
without a Linux VM. The existing Linux VM and external Docker backends remain
selectable with separate stores. Being the default does not expand compatibility
or establish a production isolation boundary.

Builds, arbitrary pulls, image deletion, terminal attach, streaming logs, volumes,
bridge networking, DNS, port publishing, Compose, migration and resource statistics
are unavailable for VM-free. M24+ execution modes are not enabled automatically
through this API. M33 preview and M80 production acceptance gates remain open.
No Docker Desktop RAM, CPU or battery savings have been established.

## Developer build

Build the helper on Apple Silicon with Go 1.22+, Xcode Command Line Tools
and Python 3 for development build scripts. The native runtime is signed with
`com.apple.security.hypervisor` (ad-hoc locally, Developer ID for releases).
PyInstaller is no longer used. In the vmfree-runtime checkout:

```sh
python scripts/bundle_dockiva_engine.py --output /path/to/dockiva/bin
```

Then build/package Dockiva normally. Its macOS packaging task copies
`bin/vmfree-engine` into `Contents/Resources` before signing and fails if the helper
is missing. Source builds can use the absolute `DOCKIVA_VMFREE_HELPER` override.
This build-time Python dependency is not an end-user dependency. The bundle must
be built and qualified for each supported macOS version; this initial local build
has not established the older deployment targets advertised by Dockiva.

## Validation

`go test ./...` covers default settings and startup errors. Set
`DOCKIVA_TEST_BUNDLED_HELPER` to the helper executable and run
`go test . -run VMFreeBundled -v` for startup, reuse, seeded-image execution and
restart with Homebrew/Python removed from PATH. The runtime project's
`scripts/verify_dockivad.py` exercises crash recovery, concurrent admission, old-state
migration and the absence of Python in the live worker tree.
The frontend must pass `npm run build` before packaging. Ad-hoc local signing is
not Developer-ID signing, notarization or clean-Mac distribution qualification.

## Persistent HTTP demo

In Containers select **HTTP server demo**, then Create with auto-start enabled. The preset uses `vmfree/server:demo`, command `["18080"]` and no port mappings. Open http://127.0.0.1:18080/ in a browser. Use another listening port if occupied. Stop closes the listener; Start and Restart restore it. The demo stays running subject to existing instruction/CPU limits. The Hello preset finishes successfully with exit code 0.

The bundled integration test verifies HTTP content, Running status, Stop listener closure, Start and Restart on a dynamically assigned port.

## Prepared Redis test preset (2026-09-22)

The Redis preview button selects `vmfree/redis:preview`, a prepared Redis 7.4.2
fixture. Since 2026-10-12 it runs on the native backend (it previously used the
removed Unicorn adapter); hosts without Hypervisor.framework reject it. Create with Command and Ports
empty, then connect a Redis client to `127.0.0.1:16379`. Stop/Start and Restart
retain its private `/data`; Remove deletes it. The Stop control terminates the
runtime, so this tests the acknowledged-write AOF recovery path, not graceful
Redis shutdown. No Docker or VM fallback is used.

It is not production, PostgreSQL or Valkey qualification. No memory/battery
savings claim. The pinned Redis preset requires a writable private root. A running old engine
is reused, so the separate Redis test launcher uses its own state directory.

## Go control-plane migration — 2026-09-22

The supported preview API now runs entirely in Go/C. Authenticated API startup,
OCI imports, registry digest checks, lifecycle, snapshot logs, stale-supervisor
reconciliation and state compatibility are implemented. Scope and evidence are in
[the migration report](https://github.com/Tomnerb/vmfree-runtime/blob/main/docs/DOCKIVA-INTEGRATION.md#dockivad-go-migration). General Docker
features, machine-wide pressure management and production isolation are still open.

The separate `bin/Launch Dockiva Go Test.command` starts `dockiva-go-test.app` with
its own `VMFREE_HOME`, retaining the automatic engine startup preference. Existing
saved runtime selections remain respected. No installed app or existing container
store is replaced by this test build.

## Execution profiles — 2026-09-22

The new helper selects profiles by verified OCI digest, queries native runtime
capabilities before guest execution, and exposes `DockivaCompatibility` in the
existing Inspect response. Image aliases preserve the selected backend; image
names do not grant capabilities. Saved policies survive daemon restarts without
silent backend/budget changes. Existing unprofiled containers retain legacy state.
All compatibility results remain EXPERIMENTAL.

Use `bin/Launch Dockiva Profiles Test.command` for the latest isolated build,
`bin/dockiva-profiles-test.app`, with `~/.vmfree-dockiva-profiles-test` state.
Select VM-free if an existing preference chooses another engine.
[Implementation and evidence](https://github.com/Tomnerb/vmfree-runtime/blob/main/docs/DOCKIVA-INTEGRATION.md#compatibility-profiles).
