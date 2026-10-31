#!/usr/bin/env bash
set -euo pipefail
cargo build --locked --release --target wasm32-wasip1 --manifest-path internal/plugin/testdata/probe-rust/Cargo.toml
cargo build --locked --release --target wasm32-wasip1 --manifest-path internal/plugin/testdata/hang-rust/Cargo.toml
cargo test --workspace --all-targets --locked
cargo build --bin termcade --locked
python3 scripts/test_rust_cli.py --binary "${CARGO_TARGET_DIR:-target}/debug/termcade" --wasm "${CARGO_TARGET_DIR:-target}/wasm32-wasip1/release/termcade_probe_fixture.wasm" --expected-version "${VERSION:-dev}"
python3 scripts/test_installer.py
python3 -m pip install --disable-pip-version-check --no-cache-dir --require-hashes --target .artifacts/pty-python -r scripts/test-requirements.txt
PYTHONPATH=.artifacts/pty-python python3 scripts/test_rust_tui.py --binary "${CARGO_TARGET_DIR:-target}/debug/termcade" --wasm "${CARGO_TARGET_DIR:-target}/wasm32-wasip1/release/termcade_probe_fixture.wasm"
