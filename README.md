<!-- Generated from private documentation source. Do not edit directly. Source SHA256: 91a456efd106e708c59da4875be255336208638bab67ce4e9a3802ac7dd2ca08 -->

# termcade

A terminal arcade with sandboxed WebAssembly games. It includes Asteroid, Tetris and Brickough. Use a truecolor terminal at least 80×24 cells.

## Install

Linux and macOS (x86_64 or arm64), using prebuilt binaries:

With curl:

```sh
curl -fsSL https://termca.de/install | sh
```

Or with mise:

```sh
mise use --global github:aviorstudio/termcade@latest
```

The shell installer verifies the release checksum and installs to `~/.local/bin`; add that directory to your PATH if necessary. Mise manages its own installation directory and PATH through its shell activation.

## Play

```sh
termcade
termcade add aviorstudio/tetris
termcade --help
```

`TERMCADE_PIXELS` selects `quad`, `sextant`, `half` or `ascii` rendering. The in-app Settings screen saves a preferred pixel style.

To create a game, run `termcade dev new you/mygame`, then use `termcade dev build <directory>` to build its package. The Go SDK is a separate module at `github.com/aviorstudio/termcade/sdk`; choose an exact `sdk/v*` tag from Releases.

## Contributor verification

Tool versions and native tasks live in `mise.toml`. Prepare the pinned helper source and run the complete gate:

```sh
git clone https://github.com/nicodes/tools.git .ci-tools
git -C .ci-tools checkout --detach 091101094fedf5ce2e67c339c1c9c842d6a662dc
mise install
mise run check
```

Native build and test commands retain source and output checks, with receipts in `.artifacts/verification`. Existing product release rules and artifacts remain owned by this repository. End users install the product’s published distribution.
