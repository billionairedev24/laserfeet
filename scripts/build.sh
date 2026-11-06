#!/usr/bin/env bash
# Builds lf for every platform plus the website, then writes SHA256SUMS.
# Builds are reproducible: the same source and Go version give byte-identical files.
set -euo pipefail
cd "$(dirname "$0")/.."

ldflags="-s -w -buildid="
if [[ "${GITHUB_REF_TYPE:-}" == "tag" ]]; then
  ldflags+=" -X main.version=${GITHUB_REF_NAME#v}"
fi
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
cp site/laserfeet.wasm site/public/
cp site/public/index.html site/laserfeet.wasm dist/

(cd dist && sha256sum -- * > SHA256SUMS)
cat dist/SHA256SUMS
