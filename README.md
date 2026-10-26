<!-- Generated from private documentation source. Do not edit directly. Source SHA256: 409b6c0b9be470e71a679e85eb2b2d51d2a0d4da678f797aca34b589f6aabc79 -->

# Termcade

A terminal arcade with sandboxed WebAssembly games. It includes Asteroid, Tetris and Brickough. Use a truecolor terminal at least 80×24 cells.

```sh
go run github.com/aviorstudio/termcade@latest
termcade add aviorstudio/tetris
termcade --help
```

Prebuilt binaries are available from [Releases](https://github.com/aviorstudio/termcade/releases). `TERMCADE_PIXELS` selects `quad`, `sextant`, `half` or `ascii` rendering. The in-app Settings screen saves a preferred pixel style.

To create a game, run `termcade dev new you/mygame`, then use `termcade dev build <directory>` to build its package. The Go SDK is a separate module at `github.com/aviorstudio/termcade/sdk`; choose an exact `sdk/v*` tag from Releases.

## Development

Install the [Komizo release](https://github.com/nicodes/komizo#install) matching `komizo.lock`.

```sh
komizo check        # Complete project checks
komizo run build    # Build the declared distributable artifacts
komizo describe     # Inspect tools and focused stages
```
