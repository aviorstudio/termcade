# termcade

A terminal arcade. Classic games rendered at sub-cell resolution with block
pixels, built on [Bubble Tea](https://charm.land/bubbletea). Every game is a
sandboxed WebAssembly package — including the three that ship with it —
and anyone writes them against the SDK and publishes them for players to
`termcade add`.

## Games

Three games are bundled, unpacked into your games directory the first time
the arcade runs:

| Game | What it is |
| --- | --- |
| **asteroid** | Asteroids-style rock shooter |
| **tetris** | falling-block stacker |
| **brickough** | Breakout-style brick breaker |

They are ordinary `.tcade` packages running in the same sandbox as anything
you install, built from source in
[termcade-games](https://github.com/aviorstudio/termcade-games) and vendored
here as packages. Nothing is compiled into the binary as a game.

Remove one and it stays removed — the arcade seeds a game once, not every
run. For anything else, press `m` for the marketplace.

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
| `ascii` | 3×3 | Pure ASCII art: a density ramp plus line characters. No block glyphs at all, so it works in any font (and any 1980s sensibility). |

```sh
TERMCADE_PIXELS=sextant go run .
```

In the arcade, `p` on the index or library cycles the pixel style and
persists it to `~/.config/termcade/settings.json`; `TERMCADE_PIXELS`
overrides the saved choice for a run. Games never change for any of this —
the renderer owns the look.

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
| l (index) | library — every game you have |
| m (index/library) | marketplace |
| r (index/library) | remove the selected added game |
| p (index/library) | cycle pixel style (incl. ASCII art) |
| q | quit (from menu) |

High scores persist to `~/.config/termcade/scores.json`, and are yours whether
or not you have an account — see [Your history](#your-history).

## The marketplace

The arcade opens on your recently played games, with the library (`l`) and
marketplace (`m`) one keystroke away. Press `m` to browse — that much is
anonymous — and sign in to install. The bundled games are what a signed-out
arcade plays; an account is what adds to them, and it is also what publishing
and the library mirror hang off. The arcade has its own sign-in/sign-up
screens, so you never need the website.

The gate is a product decision, not a security boundary: packages are public
GitHub release assets, so an account is not what keeps anyone out. What it
does is give every installed game somewhere to belong — your adds and removes
mirror to a library on your account, and `termcade sync` brings it back down
— which `termcade login` does for you, so signing in on a new machine is
enough. Sync only adds: a game you installed from a file stays put, because a
server having never heard of it is not a reason to delete it.

Signing up claims a **username** — your publishing handle, and the author
segment of every game you release. `nicodes/pong` and `aviorstudio/tetris` are
the same kind of name: the second belongs to an org, which is a studio more
than one person can publish under. Being a member is enough to publish; admin
governs the studio itself.

The same works from the command line:

```sh
termcade signup                          # create an account + claim a handle
termcade add aviorstudio/brickough       # add straight from the marketplace
termcade dev install <file>.tcade        # install a local build while developing
termcade whoami                          # who you are, and what you publish as
termcade username <name>                 # claim, rename, or check one is free
termcade org new aviorstudio             # a studio to publish under
termcade sync                            # bring your library to this machine
termcade keys new ci <username>          # a key for a release workflow
termcade list                            # what's here
termcade remove author/slug              # take one off (updates your library too)
```

## Your history

Your high scores live in `scores.json` and always have. The arcade plays with
no account and no network, and a score set that way is not waiting on either.

What an account adds is that the same history follows you. Every finished run
is recorded locally and queued; the queue drains when there is a session and a
network, which may be now, may be after you sign in, and may be next week. On
another machine, signing in brings the account's side down. Both directions
only ever raise a value — a high set here and a higher one set there both
survive — so the two can disagree, sync in either order, or never sync, and
the worst outcome is that a score stays where it was set.

The library also marks a game the marketplace has moved past, comparing the
version in the installed package's own manifest with what the catalog
publishes. `termcade add` again is what updates it.

**These are personal records, not a leaderboard.** A score comes from a game
running in your own sandbox, and nothing on the other end can check it. It is
your history, shown back to you; ranking anybody by it would need evidence
that does not exist yet, so nothing here pretends to.

There is no way to install an older version, and that is deliberate. A game
is not a dependency — nothing builds against one — so the reasons package
managers pin (reproducible builds, lockfiles, a transitive bump breaking you)
do not apply. `add` gives you what the author currently ships. The one thing
that does narrow the choice is the ABI: the registry picks the newest release
your arcade can actually run, so a game that has moved on to a later ABI tells
you to update termcade instead of handing you a package the host would refuse.

The registry is `https://api.termca.de`. `TERMCADE_REGISTRY` overrides it,
which is how you reach a local termcade-be stack (`make dev` serves one on
port 8080):

```sh
TERMCADE_REGISTRY=http://127.0.0.1:8080 termcade add you/mygame
```

**The registry stores no packages.** It is an index: a game's releases live on
its GitHub releases, and the registry records where each one is and what it
hashed to when it fetched and validated them. Publishing is open source only
for now — the registry checks that a repository is publicly visible before it
will serve anything from it.

Packages come back through the API rather than from GitHub directly. That is
what lets an install require an account and lets the arcade and the app share
one path. `termcade add` asks the registry which release to install, streams
it from the registry, and refuses to install anything whose sha256 does not
match what the registry recorded — so a release asset swapped after publishing
fails rather than reaching a player.

Installed games are WebAssembly modules that run sandboxed (no filesystem, no
network) under [wazero](https://wazero.io). A broken install shows up dimmed
on the menu with the reason; it can never take the arcade down.

## Writing games

The arcade you play with is also the whole dev kit — `termcade dev new
you/mygame` scaffolds a playable game, `termcade dev build` packages it, and
`termcade dev install build/mygame.tcade` puts it on your own menu.

To put it on everyone else's, attach the `.tcade` to a GitHub release and tell
the marketplace where it is:

```sh
termcade publish https://github.com/you/mygame v1.0.0 mygame.tcade
```

The registry fetches that asset once, validates it against the same manifest
rules the arcade enforces, reads the game's identity and version out of the
manifest inside it, and records its digest. Nothing you pass here asserts
what the package is — only where it is. The author segment of the id must be
a handle you already control: your username, claimed at signup or with
`termcade username`, or an org you belong to. Publishing under a name nobody
has claimed is refused rather than minting it.

A game is a Go package implementing `sdk.Game` — see
[docs/sdk.md](docs/sdk.md) for the author quickstart,
[docs/packaging.md](docs/packaging.md) for the manifest and `.tcade` format,
and [docs/abi.md](docs/abi.md) for the frozen wasm ABI (for non-Go
toolchains). `termcade dev build` turns a game directory into an installable
package.

[termcade-games](https://github.com/aviorstudio/termcade-games) is three
worked examples — complete games depending only on the `sdk` module, each
buildable into a `.tcade` via its `cmd/wasm` entrypoint, each published to
the marketplace the way yours will be.

## Architecture

- `sdk/` — the public, stdlib-only module game authors depend on: the `Game`
  contract (fixed 60 TPS ticks, structured HUD, 8-key input), pixel canvas
  (pluggable cell shapes, run-length ANSI batching), vector math, Bresenham
  rasterization, held-key tracking, and `sdk/tcgame`, the wasm-export glue.
  Games draw in square logical units and never see the pixel grid.
- `internal/engine` — host-side registry types and `SafeGame`, which contains
  any game panic to a crash screen instead of a dead arcade.
- `internal/plugin` — the wazero host: sandboxed instantiation, per-call
  watchdog deadlines, install-dir discovery.
- `manifest/` — `termcade.toml` parsing/validation and the `.tcade` zip
  format. Public rather than internal: the registry validates uploads
  against this same package, so a game the marketplace accepts and a game
  the arcade will run are decided by one piece of code.
- `internal/shell` — the Bubble Tea arcade frame: menu, 60 TPS tick loop that
  steps off the measured clock rather than drifting, pause/game-over/crash
  overlays.
- `internal/scores` — atomic JSON high-score persistence, namespaced by
  `author/slug`.

## Development

```sh
go build -o bin/termcade .   # local builds land in bin/ (gitignored)
go test ./...                # unit + shell tests
go test ./internal/plugin/   # includes wasm end-to-end tests (builds guests)
go vet ./...
```
