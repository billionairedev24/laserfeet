#!/usr/bin/env bash
# Builds lf for every platform plus the website, then writes SHA256SUMS.
# Builds are reproducible: the same source and Go version give byte-identical files.
set -euo pipefail
cd "$(dirname "$0")/.."

version="$(tr -d '[:space:]' < VERSION)"
ldflags="-s -w -buildid= -X main.version=$version"
flags=(-trimpath -buildvcs=false "-ldflags=$ldflags")
export CGO_ENABLED=0

rm -rf dist && mkdir -p dist
GOOS=linux   GOARCH=amd64 go build "${flags[@]}" -o dist/lf-linux-amd64     ./cmd/lf
GOOS=linux   GOARCH=arm64 go build "${flags[@]}" -o dist/lf-linux-arm64     ./cmd/lf
GOOS=darwin  GOARCH=arm64 go build "${flags[@]}" -o dist/lf-mac-arm64       ./cmd/lf
GOOS=darwin  GOARCH=amd64 go build "${flags[@]}" -o dist/lf-mac-amd64       ./cmd/lf
GOOS=windows GOARCH=amd64 go build "${flags[@]}" -o dist/lf-windows-amd64.exe ./cmd/lf

GOOS=js GOARCH=wasm go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o site/laserfeet.wasm ./cmd/wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" site/
python3 site/build.py
cp site/laserfeet.wasm install.sh install.ps1 site/public/
cp site/public/index.html site/laserfeet.wasm install.sh install.ps1 dist/

# VS Code extension, versioned to match VERSION (needs Node.js; skipped if it's missing)
if command -v npx >/dev/null 2>&1; then
  ext="$(mktemp -d)"
  cp -r editors/vscode/. "$ext/"
  python3 - "$ext/package.json" "$version" <<'PY'
import json, sys
path, version = sys.argv[1], sys.argv[2]
data = json.load(open(path))
data["version"] = version
json.dump(data, open(path, "w"), indent=2)
PY
  (cd "$ext" && npx --yes @vscode/vsce@3.6.2 package --skip-license --allow-missing-repository -o "$OLDPWD/dist/laserfeet.vsix" >/dev/null)
  rm -rf "$ext"
else
  echo "npx not found: skipping the VS Code extension" >&2
fi

# the software bill of materials: what's inside, and each file's checksum
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || echo 0)}" go run ./scripts/sbom "$version" dist

(cd dist && sha256sum -- * > SHA256SUMS)
cat dist/SHA256SUMS
