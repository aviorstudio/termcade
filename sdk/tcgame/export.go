//go:build wasip1

// Package tcgame turns a sdk.Game into a termcade wasm plugin.
//
// A game author's entire wasm entrypoint is:
//
//	package main
//
//	import (
//		"github.com/aviorstudio/termcade/sdk/tcgame"
//		mygame "example.com/me/mygame"
//	)
//
//	func init() { tcgame.Register(mygame.New) }
//	func main() {} // never runs; the module is a wasip1 reactor
//
// built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o game.wasm .
//
// The exports below are the termcade ABI v1. All pair returns pack two
// uint32s as (hi<<32)|lo. The guest owns all memory; the host only reads.
package tcgame

import (
	"encoding/json"
	"unsafe"

	"github.com/aviorstudio/termcade/sdk"
)

var (
	newGame func() sdk.Game
	game    sdk.Game
	canvas  *sdk.Canvas
	hudBuf  []byte
)

// Register stores the game constructor. Call it from an init() function —
// reactor modules never run main, but package initializers do run when the
// host calls _initialize.
func Register(new func() sdk.Game) { newGame = new }

func pack(hi, lo uint32) int64 { return int64(hi)<<32 | int64(lo) }

//go:wasmexport termcade_abi
func termcadeABI() int32 { return sdk.ABIVersion }

//go:wasmexport termcade_init
func termcadeInit(cols, rows int32) int64 {
	if newGame == nil {
		return 0
	}
	game = newGame()
	info := game.Info()
	// Only the subdivision matters guest-side; glyphs are the host's problem.
	shape := sdk.CellShape{Name: "host", Cols: int(cols), Rows: int(rows)}
	canvas = sdk.NewCanvas(info.PixelW, info.PixelH, sdk.Black, shape)
	pix := canvas.Pix()
	ptr := uint32(uintptr(unsafe.Pointer(&pix[0])))
	return pack(ptr, uint32(len(pix)*4))
}

//go:wasmexport termcade_playfield
func termcadePlayfield() int64 {
	info := game.Info()
	return pack(uint32(info.PixelW), uint32(info.PixelH))
}

//go:wasmexport termcade_reset
func termcadeReset() { game.Reset() }

//go:wasmexport termcade_key
func termcadeKey(key, down int32) {
	if down != 0 {
		game.HandleKey(sdk.Key(key))
	} else {
		game.HandleKeyUp(sdk.Key(key))
	}
}

//go:wasmexport termcade_update
func termcadeUpdate() int32 { return int32(game.Update()) }

//go:wasmexport termcade_draw
func termcadeDraw() {
	canvas.Clear()
	game.Draw(canvas)
}

//go:wasmexport termcade_score
func termcadeScore() int64 { return int64(game.Score()) }

//go:wasmexport termcade_hud
func termcadeHUD() int64 {
	b, err := json.Marshal(game.HUD())
	if err != nil || len(b) == 0 {
		return 0
	}
	hudBuf = b // keep referenced until the next call; the host copies it out
	return pack(uint32(uintptr(unsafe.Pointer(&hudBuf[0]))), uint32(len(b)))
}
