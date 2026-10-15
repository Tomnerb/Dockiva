# Dockiva Engine: the default engine on Apple Silicon

Dockiva Engine was called the VM-free engine before October 2026. Settings keep
the backend value `vmfree`, and an existing `~/.vmfree` moves to `~/.dockiva-engine`
the next time the engine starts while nothing uses it (a link stays at the old path).

Production architecture is frozen in [baseline v1.0](docs/FINAL-ARCHITECTURE-V1.md). SwiftUI/AppKit and Go/C execution without Python are required migrations; this page describes the current Vue/Wails and bundled-Python preview. The runtime remains experimental. Valkey V0–V11 is the first major compatibility gate, PostgreSQL P0–P12 the second; both remain unaccepted. Existing Redis tests remain regression protection. Runtime semantics must use generic Linux state objects without named-application compatibility branches.

## Current state (2026-10-14)

This section supersedes older capability statements further down the page.

- **Execution backends.** New containers from unpinned images run **natively** under
  Hypervisor.framework (`dockiva-hvf`): the CPU executes guest ARM64 code directly,
  with no guest kernel or Linux VM; every Linux system call is served by the
  runtime. Where a VM cannot be created (inside VMs, CI runners, missing
  entitlement), the engine falls back to the interpreter. The Unicorn backend has
  been removed. Inspect shows the choice in `DockivaCompatibility.Backend`.
- **Images.** `linux/arm64` images can be pulled from registries (including Docker
  Hub), with OCI or Docker v2 manifests. Layers stream to disk and each image is
  unpacked once; containers clone it (APFS `clonefile`), so image size is limited
  only by the disk. Symlinks, hardlinks, `#!` entrypoints (also for `exec` and
  healthchecks), any `WorkingDir` and declared volumes (as anonymous volumes) are supported; an image's `USER` (or `docker run -u`)
  is resolved from its `/etc/passwd` and `/etc/group`, and its `HEALTHCHECK` runs.
- **Networking.** The default bridge and user networks (`docker network create`)
  give each container an address in `10.89.N.0/24` and private ports: two
  containers can both listen on 5432, and unpublished ports stay off the Mac.
  On a user network (Compose's default) containers reach each other by name and
  `--network-alias` through `/etc/hosts`; `host.docker.internal` is the Mac.
  `--network host` shares the Mac's network (servers stay on its loopback);
  `none` keeps a container to its own loopback. One network per container.
  Outbound IPv4 TCP and UDP reach the internet, and DNS uses the Mac's IPv4
  nameservers. Inside a container IPv6 works on loopback and dual-stack
  (`::`, `::1`, v4-mapped addresses). Published ports (`-p`, ranges, `-P`,
  `/udp`) listen on 127.0.0.1 unless a host address is given
  (`-p 0.0.0.0:8080:80`, `-p [::]:8080:80`); `dockiva-engine
  --default-bind-ip 0.0.0.0` makes all interfaces the default, as in Docker.
  Unix-domain sockets (stream and datagram, `socketpair`) live as socket files
  in the container's root, and descriptors pass between processes (`SCM_RIGHTS`);
  abstract names are shared by a container and its `exec`s. `ping` works
  (ICMP datagram sockets, and raw ICMP for root); other raw and packet sockets
  return `EPERM`, as for a container without `CAP_NET_RAW`.
- **Docker CLI.** The engine registers a `dockiva` Docker context (it never
  switches the current one), served by `~/.dockiva-engine/docker.sock`; access is limited
  to your user by file permissions, as with Docker's own socket. After
  `docker context use dockiva`, `docker run` (foreground or `-d`, `-i`, `-t`,
  `--rm`, `-p`, `-e`, `-u`, `-m`, `--cpus`, `-v`, `--mount`, `--read-only`,
  `--network`, `--restart`, `--health-*`), `exec` (`-it`, `-u`, `-w`, `-e`, `-d`),
  `attach`, `logs` (`-f`, `-t`, `--since`, `--until`, `--tail`; stdout and
  stderr in the order written), `kill`, `wait`, `events` (Docker's container and
  image events, with filters), `ps`, `inspect`, `cp`, `stats`, `top`, `pause`,
  `unpause`, `update` (`-m`, `--cpus`, `--restart`), `rename`, `pull`, `push`
  (`--all-tags`, registry sign-in), `tag`, `rmi`, `rm`, `docker volume`,
  `docker network` and
  `docker compose` (`up`, `down`, `ps`, `logs`, `exec`, `stop`, `start`,
  `restart`, with healthchecks and `depends_on` conditions) work with the plain
  docker CLI. `-t` gives a pseudo-terminal (line editing, ^C to the foreground
  job, resizes). An `exec` is a second runtime on the container's root, mounts
  and network, so it shares files, volumes, localhost ports and Unix sockets
  (`psql`, `redis-cli`, shells) but cannot see or signal the container's own
  processes. Restart policies and healthchecks are run by each container's
  supervisor; `always` and `unless-stopped` containers start again with the engine.
- **Builds.** `docker build` runs Dockerfiles through Docker's classic builder
  API: `FROM` (multi-stage, `AS`, `scratch`, `--platform=linux/arm64`), `RUN`
  (shell and exec form, in a container with the stage's network access), `COPY`
  and `ADD` (globs, `--from` a stage or image, `--chmod`, ADD of URLs and of
  tar/gzip/bzip2 archives), `ENV`, `ARG` and `--build-arg`, `WORKDIR`, `USER`,
  `LABEL`, `EXPOSE`, `CMD`, `ENTRYPOINT`, `VOLUME`, `HEALTHCHECK`, `SHELL`,
  `STOPSIGNAL`, `--target`, `-t`, `-q`, `.dockerignore`, `docker build - <
  Dockerfile`, and `docker compose build` / `up --build`. An image is its base
  image's layers plus one layer of the build's changes; an untagged build is
  found by its ID (`docker run <id>`). Unchanged steps come from a step cache
  (`--no-cache` skips it, `docker builder prune` clears it). Plain `docker build`
  on Docker CLI 23 and later works through buildx's built-in `docker` driver (the
  engine answers its BuildKit API with the same builder); `DOCKER_BUILDKIT=0`
  also works. `RUN --mount` (`type=cache`, `bind`, `tmpfs`, and `secret` from
  `docker build --secret`), `RUN --network`, heredocs (`RUN <<EOF`,
  `COPY <<EOF`), `# syntax=docker/dockerfile:1` and `docker build --push` work.
  Not yet: `--mount=type=ssh`, `--build-context`, `--output` other than the image
  store, Git or URL build contexts, xz archives in `ADD`.
- **Mounts and volumes.** `-v /host/path:/path[:ro]` binds a host directory or a
  single file; `-v name:/path` and `-v /path` use named and anonymous volumes
  stored in `~/.dockiva-engine/volumes`. A new volume first receives the image's content
  at its mount point, read-only mounts refuse writes, and symlinks or `..` inside
  a mount cannot reach the rest of the Mac. Postgres keeps its data in a named
  volume across container removal.
- **Containers.** Private writable root by default; TCP port publishing on
  `127.0.0.1` (`HostPort:ContainerPort`); `/dev/null`, `/dev/zero`, `/dev/urandom`;
  process trees with fork/exec, threads (native backend), epoll (including
  edge-triggered), user switching (`setpriv`, `su-exec`).
- **Compatibility sweep:** 21 popular images start through `docker run` and answer
  a client: nginx, httpd, caddy, traefik, memcached, node (Alpine and Debian, and
  `npm`), python (Alpine and Debian), golang (`go build`), mariadb 11, mysql 8.4,
  mongo 7, rabbitmq 3 (management), apache/kafka 3.8, eclipse-temurin 21 (default
  and 2 GiB heaps), prometheus, grafana and elasticsearch 8.
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
  cross-process futexes, `signalfd`, `eventfd`, `timerfd`, `rt_sigtimedwait`
  (tini), named pipes (`mkfifo`), `setsid` and sessions, `/proc/self/exe`,
  `/dev/fd/N`, setuid/setgid/sticky mode bits (a set-ID program runs with the
  owner's effective ID), `mremap`, `MAP_FIXED` anywhere in the address space,
  `openat2`, `execveat`, `close_range`, and `/proc` files for each process
  (`stat`, `statm`, `status`, `task/`, with real CPU time and resident memory)
  and the container (`stat`, `meminfo`, `vmstat`, `loadavg`, `uptime`,
  `cpuinfo`, `mounts`), so `top`, `htop`, `ps`, `free`, `uptime`, `vmstat` and
  `pgrep` work. .NET SDK builds (MSBuild with worker nodes) run in `docker run`.
  Descriptors take the lowest free number; unknown system calls return `ENOSYS`.
- **Limits:** 1024 live processes per container and 128 threads per process; up to
  1024 sockets, 1024 files and 509 pipe/epoll descriptors per process (guests see
  `RLIMIT_NOFILE` 4096), 1024 pipes and 128 `signalfd`s per container, 4096 host
  descriptors per container; 8 MiB stacks; a memory limit per container (`-m`,
  6 MiB to 64 GiB; 2 GiB by default), charged as pages are first written and
  reported by `free` and `/proc/meminfo` (a 12 GiB mmap arena per process, with
  larger `PROT_NONE` reservations such as MariaDB's buffer pool placed above it;
  `munmap` and `MADV_DONTNEED` return memory; a process over the limit is killed
  with a fault rather than an OOM kill); 64 running containers
  (`dockiva-engine --max-containers`). Files are capped at 1 TiB each; the
  Mac's disk is the only storage limit. Executables up to 512 MiB.
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
  | TPC-B, 1 client | 2,213 tps | 822 tps |
  | TPC-B, 10 clients | 3,824 tps | 1,065 tps |
  | TPC-B, 50 clients | 2,588 tps | 627 tps |
  | Select-only, 10 clients | 66,115 tps | 44,607 tps |
  | Select-only, 50 clients | 67,083 tps | 68,085 tps |
  | New connection per transaction, 10 clients | 615 tps | 347 tps |

  The write-heavy lead is not a durability-equivalent comparison: guest `fsync`
  maps to macOS `fsync`, which does not force a drive-cache flush
  (`F_FULLFSYNC`), and the VM's disk stack may flush differently. Connection
  setup (a fork per client) beats the VM with 10 clients: page tables after a
  fork are updated region by region without any lock, and file opens, `stat`,
  writes and `fsync` skip the runtime's global lock (so a WAL flush no longer
  stalls other vCPUs). TPC-B at scale 1 drops past 10 clients on any system:
  every transaction updates the same branch row. Fork and exit still take the
  global lock.
- **Memory snapshots.** With several vCPUs touching pages at once, a write to
  the source of a Mach copy-on-write snapshot can show through in the
  snapshot (reproducible with Hypervisor.framework and `mach_vm_remap` alone,
  without the runtime). The runtime therefore never writes a snapshot's source
  again: every holder copies it before its first write. `fork_churn_guest`
  covers this with eight vCPUs.
- **Clocks.** The native backend maps a small vDSO, so musl and glibc read
  `CLOCK_REALTIME`/`CLOCK_MONOTONIC` and `gettimeofday` without a system call.
  Guest monotonic time excludes host sleep, as on Linux.

New Dockiva installations on Apple Silicon Macs default to **Dockiva Engine —
Experimental**. Opening Dockiva starts its bundled engine when “Start engine on
launch” is enabled (the default). Selecting Dockiva Engine in Engine Settings also starts
it. Saved engine choices and a disabled startup preference are preserved. Intel
Macs retain the Linux VM default; Windows and Linux defaults are unchanged.

The complete app includes Go dockivad (API, OCI preparation and supervisors),
C worker guards, both native ARM64 execution backends and three prepared demos.
It contains no CPython runtime and launches no Python workers. Users need no
terminal commands or runtime checkout. A running engine is
reused. Engine startup failure stays on the Dockiva Engine backend and shows an error;
there is no automatic fallback to Docker or a Linux VM.

Create a container using `dockiva/shell:demo` with command JSON
`["-c", "busybox echo hello"]`, leaving ports empty. The UI supports read-only
creation, list/inspect/start/stop/restart/remove and stdout/stderr snapshots.
Reopen logs to refresh. The API returns stdout then stderr; cross-stream ordering
is not promised. Engine and container supervisors continue when the UI closes,
so reopening reconnects to the same store. The API is not currently stopped by
Dockiva's managed-VM Stop button.

Default state lives in `~/.dockiva-engine`; `DOCKIVA_ENGINE_HOME` (or the older `VMFREE_HOME`) overrides it for developer tests.
Dockiva authenticates over a private Unix socket and reloads its token per request.
Startup diagnostics are in `~/.dockiva-engine/dockiva-engine.log`. A missing bundled helper
is a packaging error, not a request for the user to install Python.

## Project scope

Dockiva is the container workspace. dockiva-engine is its separate experimental
ARM64 Linux user-mode compatibility engine, executing bounded programs on macOS
without a Linux VM. The existing Linux VM and external Docker backends remain
selectable with separate stores. Being the default does not expand compatibility
or establish a production isolation boundary.

Builds, tmpfs mounts, more than one network per container and migration are
unavailable for Dockiva Engine. M24+ execution modes are not enabled automatically
through this API. M33 preview and M80 production acceptance gates remain open.
No Docker Desktop RAM, CPU or battery savings have been established.

## Developer build

Build the helper on Apple Silicon with Go 1.22+, Xcode Command Line Tools
and Python 3 for development build scripts. The native runtime is signed with
`com.apple.security.hypervisor` (ad-hoc locally, Developer ID for releases).
PyInstaller is no longer used. In the dockiva-engine checkout:

```sh
python scripts/bundle_dockiva_engine.py --output /path/to/dockiva/bin
```

Then build/package Dockiva normally. Its macOS packaging task copies
`bin/dockiva-engine` into `Contents/Resources` before signing and fails if the helper
is missing. Release builds do the same in CI: the macOS arm64 job checks out
dockiva-engine at the commit in `DOCKIVA_ENGINE_REF` (using the
`VMFREE_RUNTIME_TOKEN` secret, read access to that private repository) and
bundles the engine before packaging; update the file to ship a newer runtime. Source builds can use the absolute `DOCKIVA_ENGINE_HELPER` override.
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

In Containers select **HTTP server demo**, then Create with auto-start enabled. The preset uses `dockiva/server:demo`, command `["18080"]` and no port mappings. Open http://127.0.0.1:18080/ in a browser. Use another listening port if occupied. Stop closes the listener; Start and Restart restore it. The demo stays running subject to existing instruction/CPU limits. The Hello preset finishes successfully with exit code 0.

The bundled integration test verifies HTTP content, Running status, Stop listener closure, Start and Restart on a dynamically assigned port.

## Prepared Redis test preset (2026-09-22)

The Redis preview button selects `dockiva/redis:preview`, a prepared Redis 7.4.2
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
[the migration report](https://github.com/Tomnerb/dockiva-engine/blob/main/docs/DOCKIVA-INTEGRATION.md#dockivad-go-migration). General Docker
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
Select Dockiva Engine if an existing preference chooses another engine.
[Implementation and evidence](https://github.com/Tomnerb/dockiva-engine/blob/main/docs/DOCKIVA-INTEGRATION.md#compatibility-profiles).
