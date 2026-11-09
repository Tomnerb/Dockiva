# Self-hosted CI runner on Dockiva

CI's `Build and test` job (`.github/workflows/ci.yml`) runs on a self-hosted
GitHub Actions runner with the label `dockiva`. The runner is GitHub's
`actions/runner` (linux/arm64) in a Dockiva container on a maintainer's Mac.

## How it works

- `Dockerfile` builds `dockiva-gh-runner:<version>`: Ubuntu 24.04 and the
  pinned runner release (SHA-256 checked). The runner runs as root (`sudo`
  still works for steps that call it); see the Dockerfile for why.
- `run-dockiva-runner.sh` loops on the Mac: it asks GitHub for a single-use
  JIT runner config (`generate-jitconfig`) with the Mac's `gh` login, runs one
  job in a fresh container (`docker run --rm`), then starts over. Each job gets
  a clean container, and no GitHub credential lives in the image.
- A launchd agent keeps the loop running. When Dockiva is stopped, the loop
  waits; jobs stay queued on GitHub until a runner comes back (GitHub fails a
  job that waits more than 24 hours).

## Which runs use it

```yaml
runs-on: ${{ (github.event.pull_request.head.repo.fork || vars.CI_RUNNER == 'hosted') && 'ubuntu-24.04' || 'dockiva' }}
```

- Pull requests from forks always run on GitHub-hosted `ubuntu-24.04`, so
  outside code never runs on the Mac.
- Pushes to `main` and pull requests from branches in this repository run on
  Dockiva.
- Fallback: set the repository variable `CI_RUNNER` to `hosted` to send every
  run to `ubuntu-24.04` (for example, when the Mac is away for a while):

  ```sh
  gh variable set CI_RUNNER --body hosted --repo Tomnerb/Dockiva
  gh variable delete CI_RUNNER --repo Tomnerb/Dockiva   # back to Dockiva
  ```

  Runs that are already queued keep their runner; re-run them after the switch.

`release.yml` stays on GitHub-hosted runners: its macOS and Windows jobs can't
run in Linux containers, and its protected environments sign and publish.

## Setup (once per Mac)

Needs Dockiva running (docker context `dockiva`) and `gh` logged in as a user
with admin access to the repository (`gh auth status`; scope `repo`).

```sh
cd scripts/ci-runner
sed "s|__SCRIPT__|$PWD/run-dockiva-runner.sh|" dev.dockiva.gh-runner.plist \
  > ~/Library/LaunchAgents/dev.dockiva.gh-runner.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.dockiva.gh-runner.plist
```

The first start builds the image (about 2 minutes). Check it:

```sh
tail -f /tmp/dockiva-gh-runner.log
gh api repos/Tomnerb/Dockiva/actions/runners --jq '.runners[] | [.name, .status, .busy] | @tsv'
```

Stop it:

```sh
launchctl bootout gui/$(id -u)/dev.dockiva.gh-runner
```

Upgrade the runner: change `RUNNER_VERSION` and `RUNNER_SHA256` in the
Dockerfile (checksum from the release notes), and `IMAGE` in the script.

## Limits

- The job container gets 8 GiB (`MEMORY` in the script; Dockiva's default is
  2 GiB, too little for a cold cgo `go test ./...`).
- One runner, one job at a time: concurrent runs queue.
- The runner is arm64; GitHub-hosted `ubuntu-24.04` is amd64.
- Repository settings keep "Require approval for first-time contributors"
  on, as a second guard for fork PRs.
