#!/usr/bin/env bash
set -euo pipefail
cargo fmt --all --check
cargo fmt --manifest-path internal/plugin/testdata/probe-rust/Cargo.toml --check
cargo fmt --manifest-path internal/plugin/testdata/hang-rust/Cargo.toml --check
cargo clippy --workspace --all-targets --locked -- -D warnings
actionlint
shellcheck scripts/*.sh .github/scripts/*.sh
