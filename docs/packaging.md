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
An installed game whose id equals a builtin's id **shadows** the builtin —
that is also how bundled games will receive marketplace updates.

## .tcade packages

A `.tcade` is a zip with exactly two entries at its root:

```
mygame.tcade
├── termcade.toml
└── game.wasm
```

`termcade dev build [dir]` produces one from a game directory (it compiles
`./cmd/wasm`, validates the module's exports, and zips it with the manifest).

## Installing

```sh
termcade add mygame.tcade          # from a file
termcade add https://…/mygame.tcade  # from a URL
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

## Marketplace notes

The manifest is designed to double as a registry index entry: `id` is the
package name, `version` its release, and a future termcade.com needs nothing
a `.tcade` doesn't already carry. Publishing is not built yet; when it is,
`termcade add author/slug` will resolve through the registry the same way
`add <url>` works today.
