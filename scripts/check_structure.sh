#!/usr/bin/env bash
#
# Enforces the repository structure convention documented in docs/STRUCTURE.md.
# Currently checks the unambiguous, deterministic rule: no provider or wire
# directory carries a redundant cloud prefix (the parent aws/azure/gcp already
# encodes the cloud). Run from the repo root.
set -euo pipefail

fail=0

for cloud in aws azure gcp; do
  for layer in providers server; do
    dir="$layer/$cloud"
    [ -d "$dir" ] || continue
    for path in "$dir"/*/; do
      [ -d "$path" ] || continue
      name="$(basename "$path")"
      case "$name" in
        "$cloud"?*)
          echo "structure: '$dir/$name' has a redundant '$cloud' prefix — drop it (see docs/STRUCTURE.md §2.2)"
          fail=1
          ;;
      esac
    done
  done
done

if [ "$fail" -ne 0 ]; then
  echo ""
  echo "Structure check failed. See docs/STRUCTURE.md for the naming convention."
  exit 1
fi

echo "structure: no cloud-prefixed provider/wire directories — OK"
