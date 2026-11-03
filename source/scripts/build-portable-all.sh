#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
sh scripts/build-portable-windows.sh
sh scripts/build-portable-macos.sh arm64
inputs=$(mktemp -d "$root/dist/portable-inputs.XXXXXX")
trap 'rm -rf "$inputs"' EXIT HUP INT TERM
cp dist/windows-python-portable/MarketGO-Windows-x64.zip "$inputs/"
cp dist/macos-native-portable/MarketGO-macOS-arm64.zip "$inputs/"
python3 scripts/build-portable-universal.py --source "$inputs"
