#!/usr/bin/env sh
# Removes local build artefacts (P5-1). Build outputs belong in bin/ only.
# Usage: sh scripts/clean.sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
for t in bin coverage coverage.out ./*.exe ./*.test ./*.out; do
  if [ -e "$t" ]; then
    echo "removing $t"
    rm -rf -- "$t"
  fi
done
