#!/bin/sh
# Keeps one ephemeral GitHub Actions runner (label "dockiva") available in a
# Dockiva container. Each loop: mint a single-use JIT config with the host's
# `gh` login, run one job in a fresh container, remove the container.
# No GitHub credential is stored in the image or container.
set -u

REPO=${REPO:-Tomnerb/Dockiva}
CONTEXT=${DOCKER_CONTEXT:-dockiva}
IMAGE=${IMAGE:-dockiva-gh-runner:2.338.2}
# Dockiva gives containers 2 GiB by default; a cold cgo `go test ./...` needs more.
MEMORY=${MEMORY:-8g}
HERE=$(cd "$(dirname "$0")" && pwd)
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

docker_() { docker --context "$CONTEXT" "$@"; }

while :; do
  # Dockiva not running: wait for it. Queued jobs wait on GitHub meanwhile.
  if ! docker_ info >/dev/null 2>&1; then sleep 30; continue; fi
  if ! docker_ image inspect "$IMAGE" >/dev/null 2>&1; then
    docker_ build -t "$IMAGE" "$HERE" || { sleep 60; continue; }
  fi

  name="dockiva-$(hostname -s)-$(date +%s)"
  jit=$(gh api -X POST "repos/$REPO/actions/runners/generate-jitconfig" \
        -f name="$name" -F runner_group_id=1 -f work_folder=_work \
        -f 'labels[]=dockiva' --jq .encoded_jit_config) || { sleep 60; continue; }

  echo "$(date '+%F %T') $name waiting for a job"
  docker_ run --rm -m "$MEMORY" --name "$name" "$IMAGE" --jitconfig "$jit" || sleep 10
done
