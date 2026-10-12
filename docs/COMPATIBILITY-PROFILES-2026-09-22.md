# Digest-bound execution profiles — 2026-09-22

Dockivad now selects an execution profile from the verified OCI manifest digest.
The Redis image tag no longer selects a backend in Go. A renamed copy of the
prepared Redis image receives the same Unicorn policy; unrelated content using
that tag receives the default interpreter policy.

This implements profile selection and launch negotiation within the existing
preview scope. Every result remains EXPERIMENTAL. It is not a general static
compatibility analyzer, stock-image qualification or a completed production gate.

## Contract

The trusted deployment catalog is [profiles.json](../dockivad/profiles.json),
embedded into the Go executable. It contains schema version 1, a default bounded
interpreter profile, and profiles bound to specific prepared image digests. OCI
labels, tags and guest files cannot supply or replace a profile.

Each definition includes ID/revision, image digests, backend, required capability
names, dynamic-loader selection, writable-root requirement, guest-memory limit,
instruction budget and evidence scope. Unknown schema fields/versions, invalid
limits and ambiguous digest mappings fail validation. The bundle builder also
rejects stale catalog pins when a prepared fixture changes.

At Create, dockivad resolves the digest, selects policy and queries the selected C
executable's `capabilities` command. The response has schema version 1, runtime ABI
`dockiva-native-v1`, and the capabilities compiled into each available backend.
Interpreter-only builds do not advertise Unicorn. Probe execution has a two-second
deadline and separately bounded 16 KiB stdout/stderr. A missing executable,
backend, ABI or required capability prevents guest launch; no fallback occurs.

Capability names describe bounded implementation features: `elf64-arm64`,
`private-rootfs`, `loopback-tcp`, `bounded-dynamic-elf` and, on Unicorn,
`cooperative-threads`. They do not assert complete ISA, loader, thread, networking
or syscall compatibility. Dynamic behavior still needs conformance/workload tests.

The effective definition, its SHA-256 identity, image digest and runtime ABI are
saved in container configuration. Start validates the saved policy against the
configuration and queries the current executable again. The supervisor repeats
validation before launching its worker. A catalog update affects new containers;
it does not silently change an existing container's backend or instruction budget.
Explicit recreation is required to select a new profile revision.

Containers made before profiles remain on their saved legacy configuration.
Inspection labels them `legacy-state` and EXPERIMENTAL, with no negotiated ABI.
Their configuration and backend are not silently migrated. Old supervisors remain
controllable through the existing state/control compatibility path.

## Inspection

The existing container Inspect response adds `DockivaCompatibility` with status,
profile ID/revision, image and definition digests, ABI, backend, requirements,
policy and evidence scope. Dockiva's existing Inspect transport preserves it.
For example, the prepared image reports:

```json
{
  "Status": "EXPERIMENTAL",
  "ProfileID": "prepared-redis-7.4.2-unicorn",
  "Revision": 1,
  "RuntimeABI": "dockiva-native-v1",
  "Backend": "unicorn"
}
```

The endpoint remains authenticated. No new image-declared privilege or arbitrary
backend override was added. The default profile's reason explicitly states that
an unqualified image passing ELF validation is not proven application-compatible.

## Verification

- Twelve race-enabled Go test groups pass, including schema rejection, real C
  capability negotiation, digest aliases, saved-policy drift, legacy state and
  rejection before container publication or guest launch.
- Ten packaged profile checks pass: both native probes, alias writable-root policy,
  inspect metadata, replaced-backend rejection, no guest on failed negotiation,
  restored Unicorn execution of renamed prepared Redis, profile persistence across
  API restart, policy-drift rejection and cleanup.
- All 21 daemon recovery checks still pass, including immediate restarts, crash
  recovery, capacity admission, old state/control migration and no Python workers.
- All Dockiva Go packages pass against the final embedded helper. Bundled tests
  inspect interpreter/Unicorn profiles and exercise shell, HTTP and prepared Redis
  PING/SET/GET and AOF persistence across Stop/Start and Restart.
- Go vet, frontend/desktop build, complete app signature and launcher syntax pass.
  GUI clicks, older macOS versions and clean-Mac distribution were not tested.

Evidence: [Go tests](../evidence/dockivad-profiles/go-tests.txt),
[packaged profile checks](../evidence/dockivad-profiles/profiles.json),
[recovery checks](../evidence/dockivad-profiles/recovery.json),
[Dockiva tests](../evidence/dockivad-profiles/dockiva-tests.txt),
[source/artifact identities](../evidence/dockivad-profiles/artifact.json).

```sh
python3 scripts/bundle_dockiva_engine.py --output build/dockiva-profiles
go -C dockivad test -race -ldflags=-linkmode=external ./...
python3 scripts/verify_dockivad_profiles.py \
  --helper build/dockiva-profiles/vmfree-engine/vmfree-engine \
  --report build/dockivad-profiles.json
```

Build the native executables before running Go tests; the suite includes real
capability probes. Python remains development tooling, absent from the shipped
process tree. The separate Dockiva app is `bin/dockiva-profiles-test.app`; its
`Launch Dockiva Profiles Test.command` uses `~/.vmfree-dockiva-profiles-test` to
avoid reusing an older daemon. Existing user stores and installed apps are intact.

## Remaining control-plane work

This does not implement dependency/ELF feature analysis, per-image production
support certification, optional-feature negotiation or profile management UI.
Networking, global resource/pressure accounting and production isolation remain
open. Valkey V11/PostgreSQL P12 and RAM/CPU/battery advantages remain unqualified.
