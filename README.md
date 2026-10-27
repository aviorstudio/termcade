<!-- Generated from private documentation source. Do not edit directly. Source SHA256: 1a70da1c9844d181be16ad0f5639f235c5fcfa4b620c662ae8c6f9f6d410829e -->

# termcade

A terminal arcade with sandboxed WebAssembly games. It includes Asteroid, Tetris and Brickough. Use a truecolor terminal at least 80×24 cells.

```sh
curl -fsSL https://www.termca.de/install | sh
termcade
termcade add aviorstudio/tetris
termcade --help
```

The installer supports Linux and macOS, verifies the release checksum and installs to `~/.local/bin`. Add that directory to your PATH if necessary. `--install-dir` and `--version` can be passed through `sh -s --`. Prebuilt binaries are also available from [Releases](https://github.com/aviorstudio/termcade/releases). `TERMCADE_PIXELS` selects `quad`, `sextant`, `half` or `ascii` rendering. The in-app Settings screen saves a preferred pixel style.

To create a game, run `termcade dev new you/mygame`, then use `termcade dev build <directory>` to build its package. The Go SDK is a separate module at `github.com/aviorstudio/termcade/sdk`; choose an exact `sdk/v*` tag from Releases.

## Development

Install the [komizo release](https://github.com/nicodes/komizo#install) matching `komizo.lock`.

```sh
komizo check        # Complete project checks
komizo run build    # Build the declared distributable artifacts
komizo describe     # Inspect tools and focused stages
```
