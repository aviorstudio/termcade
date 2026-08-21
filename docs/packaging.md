# Packaging and installing games

## termcade.toml

Every game ships a manifest. The manifest — never the wasm — is the authority
on a game's identity.

```toml
[game]
id          = "you/mygame"   # <author>/<slug>, lowercase [a-z0-9-], globally unique
name        = "My Game"      # display title (menu shows it uppercased)
version     = "1.0.0"        # semver of the game
description = "One line about the game"

[requirements]
abi    = 1    # termcade wasm ABI major version (docs/abi.md)
width  = 64   # logical playfield; must match the game's Info/termcade_playfield
height = 40   # must be even

[controls]    # free-form labels, shown to players
"←/→"   = "move"
"space" = "action"
```

Validation is strict because manifests come from strangers: display strings
(name, version, description, controls) must be free of control characters
and are length-capped, and the playfield may not exceed **200×120** logical
units (the arcade allocates the canvas from these numbers).

`game.id` namespaces everything: the install directory, the high-score table,
menu identity. Two games named `tetris` by different authors never collide.
Installing a game whose id you already have replaces it — that is the whole
update story: the new copy simply takes the local one's place.

## .tcade packages

A `.tcade` is a zip with exactly two entries at its root:

```
mygame.tcade
├── termcade.toml
└── game.wasm
```

"Exactly" is enforced, not implied: a package carrying anything else — an
extra file, a second `termcade.toml` or `game.wasm` (zip permits duplicate
names), a nested or directory entry such as `assets/game.wasm`, or an
encrypted entry — is rejected outright rather than having the extra bytes
silently ignored. `termcade dev build [dir]` produces one at
`<dir>/build/<slug>.tcade` (it compiles `./cmd/wasm`, validates the module's
exports, and zips it with the manifest).

## Installing

```sh
termcade dev install mygame.tcade # local author iteration
termcade add you/mygame            # player install through the marketplace API
termcade list
termcade remove you/mygame
```

Install validates before touching anything — manifest parses, id well-formed,
ABI supported, wasm compiles and exports the termcade ABI — then extracts
atomically to the user data directory:

| OS | Location |
| --- | --- |
| Linux | `$XDG_DATA_HOME` or `~/.local/share`, then `termcade/games/<author>/<slug>/` |
| macOS | `~/Library/Application Support/termcade/games/<author>/<slug>/` |
| Windows | `%AppData%\termcade\games\<author>\<slug>\` |

At startup the arcade scans that tree. A directory that fails validation
shows up on the menu dimmed with the reason, is unlaunchable, and can never
prevent the arcade from starting.

## Publishing

The registry stores no packages. It is an index: a `.tcade` lives on one of
your GitHub releases, and publishing tells the registry where to find it.

```sh
termcade dev build
gh release create v1.0.0 build/mygame.tcade    # or upload it however you like
termcade publish https://github.com/you/mygame v1.0.0 mygame.tcade
```

Notice what the publish command does *not* say: not the game's id, not its
version, not its playfield. The registry fetches that asset once and reads all
of it out of the manifest inside — using this same package, so a game the
marketplace accepts is by construction a game the arcade will load. You say
where the bytes are; the bytes say what they are.

Three consequences worth knowing:

- **`version` must be plain semver** (`1.2.3`, optionally `v`-prefixed). The
  manifest itself only length-checks it, because the arcade never has to sort
  versions. The registry does — picking the newest release an arcade can run
  needs an order — so it is enforced at publish time rather than here.
- **A version is published once.** Re-publishing `1.0.0` is a conflict, not an
  overwrite.
- **Publishing requires a handle you already control.** The author segment of
  the manifest id must be your username (claimed at signup or with `termcade
  username`) or an org you belong to (`termcade org new`). A publish under a
  name nobody has claimed is refused, and nobody else can publish `you/*`.

## Installing from the marketplace

```sh
termcade add you/mygame
```

There is no pinning — `termcade add you/mygame@1.0.0` is refused. `add` asks
the registry to resolve the game, passing the ABI version this arcade speaks,
and the registry answers with the newest release this binary can actually
run.

The package itself comes back through the registry, not from GitHub — that is
what lets an install require an account. `add` checks the sha256 of what
arrives against the digest the registry recorded when it fetched and
validated the package. A mismatch aborts the install: a release asset can be
deleted and re-uploaded under the same tag, so the digest is what ties what
arrives to what was actually reviewed. Nothing installs unverified.
