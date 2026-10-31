#!/usr/bin/env bash
set -euo pipefail
export VERSION="${VERSION:-dev}"
export COMMIT="${COMMIT:-$(git rev-parse HEAD)}"
export DATE="${DATE:-unknown}"
[[ "$VERSION" == dev || "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid release version' >&2; exit 1; }
test "$COMMIT" = "$(git rev-parse HEAD)"
if [ -z "${NATIVE_CLI_ARCHIVES:-}" ]; then cargo build --release --locked; fi
python3 scripts/build-native.py
python3 scripts/check_archives.py --record
