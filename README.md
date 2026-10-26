<!-- Generated from private documentation source. Do not edit directly. Source SHA256: 2230568effdc3fa062e56fd1f29bdff2a901e2086acd330f287ff9f31dac1c83 -->

# termcade

A terminal arcade with sandboxed WebAssembly games. It includes Asteroid, Tetris and Brickough. Use a truecolor terminal at least 80×24 cells.

```sh
go run github.com/aviorstudio/termcade@latest
termcade add aviorstudio/tetris
termcade --help
```

Prebuilt binaries are available from [Releases](https://github.com/aviorstudio/termcade/releases). `TERMCADE_PIXELS` selects `quad`, `sextant`, `half` or `ascii` rendering. The in-app Settings screen saves a preferred pixel style.

To create a game, run `termcade dev new you/mygame`, then use `termcade dev build <directory>` to build its package. The Go SDK is a separate module at `github.com/aviorstudio/termcade/sdk`; choose an exact `sdk/v*` tag from Releases.

## Development

Install the [komizo release](https://github.com/nicodes/komizo#install) matching `komizo.lock`.

```sh
komizo check        # Complete project checks
komizo run build    # Build the declared distributable artifacts
komizo describe     # Inspect tools and focused stages
```
