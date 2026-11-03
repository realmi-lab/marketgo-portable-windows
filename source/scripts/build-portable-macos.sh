#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
case "${1:-arm64}" in arm64) ;; *) echo 'Usage: build-portable-macos.sh [arm64]' >&2; exit 1 ;; esac
cd "$root"
exec python3 scripts/portable-python/build_package.py --platform mac-arm64
