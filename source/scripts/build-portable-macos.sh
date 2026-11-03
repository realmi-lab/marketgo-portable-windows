#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
arch=${1:-arm64}
case "$arch" in arm64|amd64) ;; *) echo 'Usage: build-portable-macos.sh [arm64|amd64]' >&2; exit 1 ;; esac
bundle="$root/dist/macos-native-portable/$arch/MarketGO"
mkdir -p "$bundle"
cd "$root/frontend"
VITE_PORTABLE_DESKTOP=true VITE_PORTABLE_PYTHON=false VITE_PORTABLE_POWERSHELL=false VITE_LLM_ENABLED=false VITE_API_URL= pnpm exec vite build --base / --outDir "$bundle/web"
cd "$root"
CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -tags timetzdata -ldflags='-s -w' -o "$bundle/marketgo" ./cmd/market-share
cp scripts/Start-MarketGO.command "$bundle/Start-MarketGO.command"
chmod +x "$bundle/Start-MarketGO.command" "$bundle/marketgo"
cp docs/portable-macos.md "$bundle/README-KO.md"
cd "$root/dist/macos-native-portable/$arch"
zip -qr -FS "../MarketGO-macOS-$arch.zip" MarketGO
cd ..
python3 - "MarketGO-macOS-$arch.zip" <<'PY'
import hashlib
from pathlib import Path
import sys
archive = Path(sys.argv[1])
archive.with_name(archive.name + '.sha256').write_text(
    f'{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n', encoding='ascii')
PY
printf 'ZIP: %s/dist/macos-native-portable/MarketGO-macOS-%s.zip\n' "$root" "$arch"
