<!-- Generated from private documentation source. Do not edit directly. Source SHA256: 5481be7f4b12a8284fafd2c497227b431e3b74b28fd34333a6fa43922b04a315 -->

# termcade

A Rust terminal arcade with a Ratatui interface and sandboxed WebAssembly games. It includes Asteroid, Tetris and Brickough. Use a truecolor terminal at least 80×24 cells; larger games may need more space.

## Install

Linux and macOS (x86_64 or arm64), using prebuilt binaries:

```sh
curl -fsSL https://termca.de/install | sh
```

Or with mise:

```sh
mise use --global github:aviorstudio/termcade@latest
```

The shell installer verifies the release checksum and installs to `~/.local/bin`; add that directory to your PATH if necessary. Mise manages its own installation directory and PATH through its shell activation. Windows x86_64 ZIP archives are available from [Releases](https://github.com/aviorstudio/termcade/releases).

## Play

```sh
termcade
termcade login
termcade add aviorstudio/tetris
termcade sync
termcade --help
```

Bundled and locally installed games work offline. Signing in uses browser approval; games added to your account follow you with `sync`. In the interface, removing a game from Library changes your account, while **Uninstall here** removes only this machine’s copy. The CLI `remove` command removes both account membership and the local copy.

Tab and Shift+Tab change focus, arrows select, Enter activates, Esc goes back or pauses, Ctrl+N opens navigation, and Ctrl+C quits. Games use arrows/WASD, Space/Z for action A, X for action B and Enter for start; each game displays its own controls.

`TERMCADE_PIXELS` selects `quad`, `sextant`, `half` or `ascii` rendering. Settings saves a preferred style for the next game start. Local scores survive offline play and sync with your account when you reconnect.

## Build a game

Install Rust 1.94 or later, then:

```sh
termcade dev new you/my-game
cd my-game
rustup target add wasm32-wasip1
termcade dev build
termcade dev install build/my-game.tcade
termcade
```

Edit `src/lib.rs` for the game and `termcade.toml` for its identity, playfield and controls. The generated Cargo project pins the Rust `termcade-sdk` crate to an exact source revision. `dev build` compiles a WASI reactor and packages it as a `.tcade`; the arcade runs it with ABI v1. Existing ABI-v1 packages remain compatible.

Publish a package already uploaded to a GitHub release:

```sh
termcade publish https://github.com/you/my-game v0.1.0 my-game.tcade
```

`termcade org` manages studio handles and membership. `termcade keys` manages scoped publishing keys for CI; `TERMCADE_TOKEN` accepts a publishing key for `publish`.

## Contributor verification

Tool versions and native tasks live in `mise.toml`. Prepare the pinned helper source and run the complete gate:

```sh
git clone https://github.com/nicodes/tools.git .ci-tools
git -C .ci-tools checkout --detach f412061a7a3f0914db9e09d1159e69504aee8706
mise install
mise run check
```

The gate checks Rust formatting, Clippy, SDK and sandbox contracts, CLI and terminal journeys, installer behavior, release archives and locked dependencies. GitHub Actions builds the five native Linux, macOS and Windows archives. Source and output receipts remain in `.artifacts/verification`.
