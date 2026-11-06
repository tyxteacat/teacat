#!/usr/bin/env sh
# Build TeaCat. Without arguments builds for the local platform; with --all
# cross-compiles every supported target into dist/.
set -eu

export CGO_ENABLED=0
OUT="${OUT:-dist}"

build_one() {
    goos="$1"
    goarch="$2"
    ext="${3:-}"
    name="teacat-${goos}-${goarch}${ext}"
    echo "building ${name} ..."
    GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath -ldflags "-s -w" -o "${OUT}/${name}" ./cmd/teacat
}

if [ "${1:-}" = "--all" ]; then
    mkdir -p "$OUT"
    build_one windows amd64 .exe
    build_one windows arm64 .exe
    build_one linux amd64
    build_one linux arm64
    build_one darwin amd64
    build_one darwin arm64
    ( cd "$OUT" && sha256sum teacat-* | sed 's#  #  #' > checksums.txt )
    echo "built binaries in ${OUT}/ (see ${OUT}/checksums.txt)"
else
    go build -trimpath -ldflags "-s -w" -o teacat ./cmd/teacat
    echo "built ./teacat"
fi
