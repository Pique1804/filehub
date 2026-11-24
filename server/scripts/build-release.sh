#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
output_dir="${1:-$PWD/releases}"
mkdir -p "$output_dir"
output_dir="$(realpath "$output_dir")"
npm --prefix frontend ci --no-audit --no-fund
npm --prefix frontend run build
GOMAXPROCS="${GOMAXPROCS:-2}" go test -count=1 ./...
GOMAXPROCS="${GOMAXPROCS:-2}" go vet ./...
python3 -m unittest -v test_original_backup.py
CGO_ENABLED=0 GOMAXPROCS="${GOMAXPROCS:-2}" go build -trimpath -buildvcs=false -o "$output_dir/filehub" .
python3 scripts/release-manifest.py "$output_dir"
