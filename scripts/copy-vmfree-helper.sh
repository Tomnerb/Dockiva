#!/usr/bin/env bash
set -euo pipefail
bundle_source="$1"
bundle_target="$2"
[[ "$(basename "$bundle_target")" == vmfree-engine ]] || exit 2
for executable in dockivad vmfree-engine _internal/build/vmfree-worker _internal/build/vmfree-c _internal/build/vmfree-c-hvf; do
  test -x "$bundle_source/$executable"
  codesign --verify --strict "$bundle_source/$executable"
done
if [[ -n "$(find "$bundle_source" \( -iname '*python*' -o -name '*.py' -o -name '*.pyc' \) -print -quit)" ]]; then
  echo 'VM-free bundle contains Python; rebuild with Go dockivad.' >&2
  exit 1
fi
# Replace the generated app resource tree so older CPython files cannot survive a rebuild.
mkdir -p "$(dirname "$bundle_target")"
rm -rf -- "$bundle_target"
cp -R "$bundle_source" "$bundle_target"
