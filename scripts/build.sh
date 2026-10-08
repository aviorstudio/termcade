#!/usr/bin/env bash
set -euo pipefail
if [[ "$VERSION" != dev && ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Unsupported version string: $VERSION" >&2
  exit 1
fi
mkdir -p dist
# linux/386 and friends excluded on purpose: wazero's compiler backend
# covers amd64/arm64, and everything else falls back to the
# interpreter — playable, but not something worth publishing.
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  GOOS="${target%/*}"; GOARCH="${target#*/}"
  bin=termcade
  [ "$GOOS" = windows ] && bin=termcade.exe
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "dist/$bin" .
  name="termcade_${GOOS^}_${GOARCH}"
  case "$GOARCH" in amd64) name="termcade_${GOOS^}_x86_64";; esac
  if [ "$GOOS" = windows ]; then
    (cd dist && zip -q "${name}.zip" "$bin" && rm "$bin")
  else
    tar -czf "dist/${name}.tar.gz" -C dist "$bin" && rm "dist/$bin"
  fi
done

set -euo pipefail
(cd dist && sha256sum -- *.tar.gz *.zip > checksums.txt)
cat dist/checksums.txt
