<!-- Generated from private documentation source. Do not edit directly. Source SHA256: 4f4784e3a138f81e52f8c4496131c6b94e728061a1248c3c4aa937f50c732e0a -->

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

`TERMCADE_PIXELS` selects `quad`, `sextant`, `half` or `ascii` rendering. Current-source builds save the preferred style in `settings.toml` in the configuration directory. A legacy `settings.json` requires explicit conversion before startup; malformed TOML fails rather than replacing the preference. Installed releases retain their released interface. Local scores survive offline play and sync with your account when you reconnect.

Current-source migration is manual: create the named TOML source with the same effective settings and archive the legacy JSON v1 source outside the active configuration directory. No automatic import command is provided. Preserve explicit `false`, zero and empty arrays; omit null overrides only where they meant inheritance or defaults. Do not copy credentials into tracked source files. Existing installed settings are not migrated by a source update.

## Machine commands

Place `--json` before a finite command, for example `termcade --json whoami`, `termcade --json list` or `termcade --json dev new you/my-game`. Stdout contains one versioned `{v, ok, result}` or `{v, ok, error}` document; progress uses stderr. API failures retain safe messages, status, code and server request ID. Help/version succeed, unknown commands exit 2 and failed operations exit 1. JSON mode refuses the default terminal interface and interactive browser login. Creating a publishing key returns its one-time token in the explicit command result; account and key-list results omit saved credentials.

Configuration follows `XDG_CONFIG_HOME` and data follows `XDG_DATA_HOME`, with native platform defaults when unset. `TERMCADE_REGISTRY` explicitly selects the registry; a saved session remains scoped to its registry. `TERMCADE_TOKEN` supplies a publishing key for `publish`.

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

`mise.toml` pins contributor SDKs. The canonical generated task block in root `mise.toml` selects an immutable shared cookbook revision and binds it to `cookbook.toml`.

```sh
mise run help
mise run setup
mise run build
mise run test
```

`mise run help` works offline before setup. `mise run setup` explicitly prepares pinned SDKs, locked dependencies and declared fixtures. `mise run build` compiles and packages the native outputs, then validates their source and artifact identity. `mise run test` runs the implemented checks against those current-source outputs. Run build again after changing source; tests do not install tools or build missing shipping artifacts.

Use `mise run fmt` to apply formatting, `mise run test:lint`, `mise run test:unit`, `mise run test:security` or `mise run test:integration` to select a group. `mise run test:e2e` runs the declared terminal journeys. `mise run clean` removes declared generated outputs. Build and test evidence stays in `.artifacts/verification`.

The verified executable is `.artifacts/build/termcade` (`termcade.exe` on Windows). Build also prepares the WASIp1 probe used by the CLI and real PTY journeys. Frozen SDK rendering, sandbox boundaries, installer behavior and native archive checks remain part of verification. CI retains five native archives: Linux and macOS on x86_64 and arm64, and Windows x86_64. Release publication remains explicit and preserves the existing archive names, checksums and signer identity.
