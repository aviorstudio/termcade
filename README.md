<!-- Generated from private documentation source. Do not edit directly. Source SHA256: 273fbf5d3738f782632f010a83018b67794fd9a7e07ba801a2b15590b97732dc -->

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
