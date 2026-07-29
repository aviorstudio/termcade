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

From the marketplace (press `m`, or the CLI):

- **BRICKOUGH** — Breakout-style brick breaker. Steer the ball with paddle
  english, clear the wall, survive the speed-up. Lives outside this repo as
  a real third-party-shaped game, installed the way any marketplace game is:

```sh
termcade add aviorstudio/brickough
```

## Run

With Go installed, straight from the internet:

```sh
go run github.com/aviorstudio/termcade@latest
```

Or grab a prebuilt archive from the
[releases](https://github.com/aviorstudio/termcade/releases). From a
checkout (toolchain pinned via [mise](https://mise.jdx.dev) — `mise
install`): `go run .`.

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
TERMCADE_PIXELS=sextant go run .
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
| m (menu) | marketplace |
| r (menu) | remove the selected added game |
| q | quit (from menu) |

High scores persist to `~/.config/termcade/scores.json`.

## The marketplace

Press `m` on the arcade menu to browse the marketplace — no account needed to
look. Adding a game to your arcade requires signing in, and the arcade has
its own sign-in/sign-up screens, so you never need the website. Your library
follows your account across machines.

The same works from the command line:

```sh
termcade signup                    # create an account (or: termcade login)
termcade add aviorstudio/brickough # add straight from the marketplace
termcade add <file-or-url>.tcade   # or from a package you have
termcade list                      # what's here
termcade remove author/slug        # take one off (updates your library too)
```

The registry defaults to local dev (`http://127.0.0.1:8080`, the termcade-be
stack); point `TERMCADE_REGISTRY` elsewhere until termcade.com is live.
Downloads are verified against the registry's sha256 before install.

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

The games under `games/` are worked examples — complete games depending
only on the `sdk` module, each buildable into a `.tcade` via its `cmd/wasm`
entrypoint. Brickough goes one further: it lives entirely outside this repo
as its own module against the published sdk, and reaches the arcade only
through the marketplace — the exact path a third-party game takes.

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
- `games/*` — every game, builtin or not: sdk-only packages with a
  `cmd/wasm` entrypoint and a `termcade.toml`, exactly the shape a
  third-party game has. Builtins are just the ones `main.go`
  also compiles in.

## Development

```sh
go build -o bin/termcade .   # local builds land in bin/ (gitignored)
go test ./...                # unit + shell tests
go test ./internal/plugin/   # includes wasm end-to-end tests (builds guests)
go vet ./...
```
