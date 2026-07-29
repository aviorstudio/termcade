package plugin

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tetratelabs/wazero/api"

	"github.com/aviorstudio/termcade/sdk"
)

const (
	// initTimeout bounds instantiate + first-frame setup; the Go runtime
	// booting inside the guest is the slow part.
	initTimeout = 5 * time.Second
	// callTimeout bounds every per-frame guest call. A guest that spins past
	// it is killed (the deadline closes the instance), the call panics, and
	// engine.SafeGame turns that panic into the crash screen.
	callTimeout = 500 * time.Millisecond
)

// wasmGame adapts one wasm module instance to sdk.Game. Errors and timeouts
// surface as panics on purpose: the shell already routes game panics through
// engine.SafeGame to a crash overlay, and a broken guest is exactly that.
type wasmGame struct {
	ctx    context.Context
	mod    api.Module
	info   sdk.Info
	pixPtr uint32
	pixLen uint32

	fnReset, fnKey, fnUpdate, fnDraw, fnScore, fnHUD api.Function
}

// start checks the ABI handshake and captures the guest's pixel buffer.
func (g *wasmGame) start(shape sdk.CellShape) error {
	abi, err := g.call1(mustFn(g.mod, "termcade_abi"), initTimeout)
	if err != nil {
		return err
	}
	if int32(abi) != sdk.ABIVersion {
		return fmt.Errorf("game speaks ABI v%d; this termcade speaks v%d", int32(abi), sdk.ABIVersion)
	}

	ret, err := g.call1(mustFn(g.mod, "termcade_init"), initTimeout,
		uint64(uint32(shape.Cols)), uint64(uint32(shape.Rows)))
	if err != nil {
		return err
	}
	g.pixPtr, g.pixLen = uint32(ret>>32), uint32(ret)
	if g.pixLen == 0 {
		return fmt.Errorf("game did not register (termcade_init returned no buffer)")
	}

	ret, err = g.call1(mustFn(g.mod, "termcade_playfield"), initTimeout)
	if err != nil {
		return err
	}
	w, h := int(uint32(ret>>32)), int(uint32(ret))
	if w != g.info.PixelW || h != g.info.PixelH {
		return fmt.Errorf("playfield is %dx%d but the manifest says %dx%d",
			w, h, g.info.PixelW, g.info.PixelH)
	}
	if want := uint32(w*shape.Cols*h*shape.Rows/2) * 4; g.pixLen != want {
		return fmt.Errorf("pixel buffer is %d bytes, want %d", g.pixLen, want)
	}

	g.fnReset = mustFn(g.mod, "termcade_reset")
	g.fnKey = mustFn(g.mod, "termcade_key")
	g.fnUpdate = mustFn(g.mod, "termcade_update")
	g.fnDraw = mustFn(g.mod, "termcade_draw")
	g.fnScore = mustFn(g.mod, "termcade_score")
	g.fnHUD = mustFn(g.mod, "termcade_hud")
	return nil
}

// call1 invokes an export under a deadline and returns its single result
// (0 for void exports). On failure the instance is dead by definition —
// either it trapped or the deadline closed it — so the error is terminal.
func (g *wasmGame) call1(fn api.Function, timeout time.Duration, args ...uint64) (uint64, error) {
	ctx, cancel := context.WithTimeout(g.ctx, timeout)
	defer cancel()
	res, err := fn.Call(ctx, args...)
	if err != nil {
		return 0, fmt.Errorf("wasm %s: %w", fn.Definition().Name(), err)
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

// invoke is call1 for the per-frame path: any failure panics, which
// engine.SafeGame converts into the crash screen.
func (g *wasmGame) invoke(fn api.Function, args ...uint64) uint64 {
	ret, err := g.call1(fn, callTimeout, args...)
	if err != nil {
		panic(err)
	}
	return ret
}

func (g *wasmGame) Info() sdk.Info { return g.info }
func (g *wasmGame) Reset()         { g.invoke(g.fnReset) }

func (g *wasmGame) HandleKey(k sdk.Key)   { g.invoke(g.fnKey, uint64(uint32(k)), 1) }
func (g *wasmGame) HandleKeyUp(k sdk.Key) { g.invoke(g.fnKey, uint64(uint32(k)), 0) }

func (g *wasmGame) Update() sdk.Status {
	return sdk.Status(int32(g.invoke(g.fnUpdate)))
}

// Draw asks the guest to rasterize, then copies its pixel buffer onto the
// shell's canvas. Wasm linear memory is little-endian by spec, so the u32
// pixels decode portably.
func (g *wasmGame) Draw(c *sdk.Canvas) {
	g.invoke(g.fnDraw)
	raw, ok := g.mod.Memory().Read(g.pixPtr, g.pixLen)
	if !ok {
		panic(fmt.Errorf("wasm pixel buffer (%d bytes at %#x) out of bounds", g.pixLen, g.pixPtr))
	}
	pix := c.Pix()
	n := min(len(pix), len(raw)/4)
	for i := 0; i < n; i++ {
		pix[i] = sdk.Color(binary.LittleEndian.Uint32(raw[i*4:]))
	}
}

func (g *wasmGame) Score() int {
	return int(int64(g.invoke(g.fnScore)))
}

func (g *wasmGame) HUD() sdk.HUD {
	ret := g.invoke(g.fnHUD)
	ptr, length := uint32(ret>>32), uint32(ret)
	if length == 0 {
		return sdk.HUD{}
	}
	raw, ok := g.mod.Memory().Read(ptr, length)
	if !ok {
		panic(fmt.Errorf("wasm HUD buffer (%d bytes at %#x) out of bounds", length, ptr))
	}
	var h sdk.HUD
	if err := json.Unmarshal(raw, &h); err != nil {
		return sdk.HUD{} // garbage HUD is cosmetic; don't kill the game over it
	}
	return h
}

func (g *wasmGame) Close() error { return g.mod.Close(g.ctx) }
