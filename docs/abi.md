# termcade wasm ABI v1

A termcade game is a **wasip1 reactor module** (`GOOS=wasip1 GOARCH=wasm
go build -buildmode=c-shared`) exporting the functions below. Go authors get
all of this from `sdk/tcgame` and never write it by hand; the ABI exists so
other toolchains (TinyGo, Rust, …) can target termcade too.

The host runs guests under [wazero](https://wazero.io) with WASI preview 1
available but **no preopened directories, args, or environment** — only the
system clock and an entropy source. Guest memory is capped at 64 MiB.
Instantiation runs `_initialize` (package initializers), never `main`.

## Conventions

- Pair returns pack two u32s into one i64 as `(hi << 32) | lo`.
- The guest owns all memory. The host only ever reads guest memory.
- Strings/buffers cross guest→host as `(ptr << 32) | byteLen` into linear
  memory. The HUD pointer is valid until the next guest call; the pixel
  buffer pointer must stay valid for the instance's lifetime.
- The host calls exports one at a time from one goroutine; no reentrancy.
- **`termcade_init` comes first.** It is what constructs the game, so every
  other export except `termcade_abi` traps until it has run — a guest written
  in Go dereferences a nil game and takes the instance down. The arcade calls
  `termcade_abi`, then `termcade_init`, then `termcade_playfield`, and any
  other host must do the same.
- Per-call budgets: 5s for setup calls, 500ms per frame call. A call that
  exceeds its budget kills the instance.

## Exports

| Export | Signature | Semantics |
| --- | --- | --- |
| `termcade_abi` | `() → i32` | ABI major version; this document is v1. Checked before anything else. |
| `termcade_init` | `(cols i32, rows i32) → i64` | Cell subdivision of the player's terminal (e.g. 2,2). Construct the game and a pixel buffer; return `(pixPtr << 32) \| pixByteLen`. Called once per instance. |
| `termcade_playfield` | `() → i64` | `(width << 32) \| height` in logical units. Must match the manifest, or the game refuses to load. |
| `termcade_reset` | `()` | Start a fresh run: score 0, first level. |
| `termcade_key` | `(key i32, down i32)` | `down=1` press, `down=0` release. Key values below. |
| `termcade_update` | `() → i32` | Advance exactly one 1/60s tick. `0` running, `1` game over. |
| `termcade_draw` | `()` | Rasterize the current state into the pixel buffer. |
| `termcade_score` | `() → i64` | Current score; read at any time. |
| `termcade_hud` | `() → i64` | `(ptr << 32) \| len` of UTF-8 JSON: `{"Fields":[{"Label":..,"Value":..,"Accent":..}],"Hint":".."}`. Return 0 for no HUD. |

## Pixel buffer

Row-major, one u32 per pixel, little-endian (wasm linear memory order),
`0x00RRGGBB`. Dimensions: `fw = width × cols`, `fh = height × rows / 2`,
so `pixByteLen = fw × fh × 4`. The host copies the buffer out after every
`termcade_draw`.

## Key values (frozen)

| Value | Key |
| --- | --- |
| 0 | none |
| 1 | left |
| 2 | right |
| 3 | up |
| 4 | down |
| 5 | A (space / z) |
| 6 | B (x) |
| 7 | start (enter) |

New keys may be appended in future ABI versions; existing values never change.
