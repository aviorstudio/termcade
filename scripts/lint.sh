#!/usr/bin/env bash
set -euo pipefail
unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
  echo "::error::gofmt would change these files:"
  echo "$unformatted"
  exit 1
fi

go vet ./...
go vet ./sdk/...

GOWORK=off go vet ./...
