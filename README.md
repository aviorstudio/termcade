# termcade

A terminal arcade. Classic games rendered at sub-cell resolution with block
pixels, built on [Bubble Tea](https://charm.land/bubbletea). Ships with
builtin games; anyone can write more against the SDK and distribute them as
sandboxed WebAssembly packages that players add with `termcade add`.

## Games

Builtin:

- **ASTEROID** — Asteroids-style rock shooter. Inertial ship on a wrapping
  field; big rocks split into faster small ones.
- **TETRIS** — Falling-block stacker. Super Rotation System kicks, a 7-bag
  randomizer, ghost piece, lock delay, and gravity that tightens by level.

Installable (the reference `.tcade` package, built from this repo):

- **BRICKOUGH** — Breakout-style brick breaker. Steer the ball with paddle
  english, clear the wall, survive the speed-up.

```sh
termcade dev build games/brickough && termcade add brickough.tcade
```

## Run

Requires Go (pinned via [mise](https://mise.jdx.dev) — `mise install`).

```sh
go run ./cmd/termcade
```

Needs a truecolor terminal at 80×24 or larger.

### Pixel density

`TERMCADE_PIXELS` picks how each terminal cell is subdivided. Every setting
draws the same playfield into the same cells; only sharpness and font
requirements change.

| Value | Pixels per cell | Notes |
| --- | --- | --- |
| `quad` (default) | 2×2 | Unicode 1.0 block elements; renders anywhere. |
| `sextant` | 2×3 | Sharpest, near-square pixels. Needs Unicode 13 (Symbols for Legacy Computing) or you get tofu. |
| `half` | 1×2 | The original look, for fonts that struggle with the rest. |

```sh
TERMCADE_PIXELS=sextant go run ./cmd/termcade
```

### Input latency

Terminals send no key-release events, so a held key is normally guessed from
auto-repeat — which stalls for the OS repeat delay (often ~500ms) after the
first press, and makes the paddle feel like it ignores you.

termcade asks for the Kitty keyboard protocol's event-type reporting. On
terminals that support it (kitty, Ghostty, foot, WezTerm, recent xterm) held
keys are exact and that stall disappears. Elsewhere it silently falls back to
the auto-repeat heuristic.

## Controls

| Key | Action |
| --- | --- |
| ↑/↓ or j/k | menu / pause navigation |
| enter | select / launch |
| ←/→ (or a/d, h/l) | move paddle / turn ship / shift piece |
| ↑ (or w) | thrust (Asteroid) · rotate (Tetris) |
| ↓ (or s) | soft drop (Tetris) |
| space or z | A button: launch ball / fire / hard drop |
| x | B button |
| esc or p | pause |
| q | quit (from menu) |

High scores persist to `~/.config/termcade/scores.json`.

## Adding games

```sh
termcade add <file-or-url>.tcade   # put a game on the menu
termcade list                      # what's here
termcade remove author/slug        # take one off
```

Installed games are WebAssembly modules that run sandboxed (no filesystem, no
network) under [wazero](https://wazero.io). A broken install shows up dimmed
on the menu with the reason; it can never take the arcade down.

## Writing games

The arcade you play with is also the whole dev kit — `termcade dev new
you/mygame` scaffolds a playable game, `termcade dev build` packages it, and
`termcade add` puts it on your own menu.

A game is a Go package implementing `sdk.Game` — see
[docs/sdk.md](docs/sdk.md) for the author quickstart,
[docs/packaging.md](docs/packaging.md) for the manifest and `.tcade` format,
and [docs/abi.md](docs/abi.md) for the frozen wasm ABI (for non-Go
toolchains). `termcade dev build` turns a game directory into an installable
package.

`games/brickough` is the worked example: a complete game that depends only on
the `sdk` module and ships purely as a `.tcade` built from its `cmd/wasm`
entrypoint — exactly the shape of a third-party game.

## Architecture

- `sdk/` — the public, stdlib-only module game authors depend on: the `Game`
  contract (fixed 60 TPS ticks, structured HUD, 8-key input), pixel canvas
  (pluggable cell shapes, run-length ANSI batching), vector math, Bresenham
  rasterization, held-key tracking, and `sdk/tcgame`, the wasm-export glue.
  Games draw in square logical units and never see the pixel grid.
- `internal/engine` — host-side registry types and `SafeGame`, which contains
  any game panic to a crash screen instead of a dead arcade.
- `internal/plugin` — the wazero host: sandboxed instantiation, per-call
  watchdog deadlines, install-dir discovery, builtin shadowing.
- `internal/manifest` — `termcade.toml` parsing/validation and the `.tcade`
  zip format.
- `internal/shell` — the Bubble Tea arcade frame: menu, 60 TPS tick loop that
  steps off the measured clock rather than drifting, pause/game-over/crash
  overlays.
- `internal/scores` — atomic JSON high-score persistence, namespaced by
  `author/slug`.
- `internal/games/*` — builtin games; each registers in
  `cmd/termcade/main.go` with one line.
- `games/*` — installable games, sdk-only packages with a `cmd/wasm`
  entrypoint and a `termcade.toml`.

## Development

```sh
go test ./...        # unit + shell tests
go test ./internal/plugin/   # includes wasm end-to-end tests (builds guests)
go vet ./...
```
