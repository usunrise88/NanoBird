#!/usr/bin/env bash
# Build the release archives of NanoBird (SPEC §11.3). Each archive holds the binary,
# LICENSE, NOTICE and README.md, flat, so that it unpacks anywhere. NanoBird compiles
# no C: every binary is CGO_ENABLED=0 and cross-compiles from one Linux runner, as
# `make cross` does.
#
#   scripts/dist.sh [TARGET...]
#
# TARGET is one of linux-amd64 linux-arm64 windows-amd64 windows-arm64; the default is
# all four. Environment:
#   VERSION  the release tag, stamped into the binary and the file names
#            (default: git describe)
#   OUT      where the archives go (default dist)
#   GO       the go command (default go)
#
# sha256sums.txt is written next to the archives. Progress goes to stderr, and the
# archive paths go to stdout, one per line.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="${OUT:-dist}"
GO="${GO:-go}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

targets=("$@")
if [ ${#targets[@]} -eq 0 ]; then
    targets=(linux-amd64 linux-arm64 windows-amd64 windows-arm64)
fi
for t in "${targets[@]}"; do
    case "$t" in
        linux-amd64 | linux-arm64 | windows-amd64 | windows-arm64) ;;
        *) echo "dist: unknown target $t" >&2; exit 2 ;;
    esac
done

mkdir -p "$OUT"
archives=()
for t in "${targets[@]}"; do
    os="${t%%-*}"
    arch="${t#*-}"
    ext=""
    if [ "$os" = windows ]; then
        ext=".exe"
    fi
    name="nanobird_${VERSION}_${os}_${arch}"
    stage="$(mktemp -d)"
    echo "build $t" >&2
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$GO" build -trimpath \
        -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT" \
        -o "$stage/nanobird$ext" ./cmd/nanobird
    cp LICENSE NOTICE README.md "$stage/"
    if [ "$os" = windows ]; then
        archive="$OUT/$name.zip"
        python3 - "$stage" "$archive" <<'PY'
import os
import sys
import zipfile

stage, archive = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as z:
    for name in sorted(os.listdir(stage)):
        z.write(os.path.join(stage, name), name)
PY
    else
        archive="$OUT/$name.tar.gz"
        tar -C "$stage" -czf "$archive" .
    fi
    rm -rf "$stage"
    archives+=("$(basename "$archive")")
done

(cd "$OUT" && sha256sum "${archives[@]}" > sha256sums.txt)
for a in "${archives[@]}"; do
    echo "$OUT/$a"
done
