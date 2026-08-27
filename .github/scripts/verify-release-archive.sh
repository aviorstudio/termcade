#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 ARCHIVE EXPECTED_OUTPUT" >&2
  exit 2
fi

archive=$1
expected=$2
workdir=$(mktemp -d)
cleanup() {
  rm -rf -- "$workdir"
}
trap cleanup EXIT

listing="$workdir/listing"
metadata="$workdir/metadata"
actual="$workdir/actual"

tar -tzf "$archive" > "$listing"
if [[ $(wc -l < "$listing") -ne 1 ]] || [[ $(<"$listing") != termcade ]]; then
  echo "archive must contain exactly one root entry named termcade" >&2
  exit 1
fi

tar -tvzf "$archive" > "$metadata"
if [[ $(wc -l < "$metadata") -ne 1 ]] || [[ $(<"$metadata") != -* ]]; then
  echo "archive entry termcade must be a regular file" >&2
  exit 1
fi

tar -xzf "$archive" -C "$workdir" --no-same-owner --no-same-permissions
if [[ ! -f "$workdir/termcade" || -L "$workdir/termcade" ]]; then
  echo "extracted termcade must be a regular, non-symlink file" >&2
  exit 1
fi
chmod u+x "$workdir/termcade"

if timeout 30s "$workdir/termcade" version > "$actual"; then
  :
else
  status=$?
  echo "termcade version command failed with exit $status" >&2
  exit "$status"
fi

if ! printf '%s\n' "$expected" | cmp -s - "$actual"; then
  echo "released binary version mismatch" >&2
  printf 'expected: %q\n' "$expected" >&2
  printf 'actual:   %q\n' "$(<"$actual")" >&2
  exit 1
fi

printf 'verified: %s\n' "$expected"
