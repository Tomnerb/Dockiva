#!/usr/bin/env bash
set -euo pipefail
bundle_source="$1"
bundle_target="$2"
[[ "$(basename "$bundle_target")" == dockiva-engine ]] || exit 2
for executable in dockivad dockiva-engine _internal/build/dockiva-worker _internal/build/dockiva-runtime _internal/build/dockiva-hvf; do
  test -x "$bundle_source/$executable"
  codesign --verify --strict "$bundle_source/$executable"
done
if [[ -n "$(find "$bundle_source" \( -iname '*python*' -o -name '*.py' -o -name '*.pyc' \) -print -quit)" ]]; then
  echo 'Dockiva Engine bundle contains Python; rebuild with Go dockivad.' >&2
  exit 1
fi
# Replace the generated app resource tree so older CPython files cannot survive a rebuild.
mkdir -p "$(dirname "$bundle_target")"
rm -rf -- "$bundle_target"
cp -R "$bundle_source" "$bundle_target"
