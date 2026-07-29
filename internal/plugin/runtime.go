// Package plugin hosts installed wasm games. Guests run sandboxed under
// wazero with no filesystem, network, args, or environment — they get a
// clock, entropy, and a pixel buffer, nothing else.
package plugin

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/aviorstudio/termcade/sdk"
)

// memoryLimitPages caps guest memory at 64 MiB (pages are 64 KiB).
const memoryLimitPages = 1024

// Runtime compiles and instantiates wasm games. One Runtime serves the whole
// arcade session; compilation is cached per module so Restart is instant.
type Runtime struct {
	ctx   context.Context
	rt    wazero.Runtime
	cache map[string]wazero.CompiledModule
}

func NewRuntime(ctx context.Context) *Runtime {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true). // this is what makes call deadlines lethal
		WithMemoryLimitPages(memoryLimitPages))
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	return &Runtime{ctx: ctx, rt: rt, cache: map[string]wazero.CompiledModule{}}
}

func (r *Runtime) Close() error { return r.rt.Close(r.ctx) }

// Compile validates and compiles a module, caching by key. It also checks
// that every ABI export is present, so installs can reject junk early.
func (r *Runtime) Compile(key string, wasmBytes []byte) (wazero.CompiledModule, error) {
	if cm, ok := r.cache[key]; ok {
		return cm, nil
	}
	cm, err := r.rt.CompileModule(r.ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compiling wasm: %w", err)
	}
	exports := cm.ExportedFunctions()
	for _, name := range abiExports {
		if _, ok := exports[name]; !ok {
			cm.Close(r.ctx)
			return nil, fmt.Errorf("wasm module missing export %q (not a termcade game?)", name)
		}
	}
	r.cache[key] = cm
	return cm, nil
}

var abiExports = []string{
	"termcade_abi", "termcade_init", "termcade_playfield", "termcade_reset",
	"termcade_key", "termcade_update", "termcade_draw", "termcade_score",
	"termcade_hud",
}

// Load instantiates a fresh game from compiled wasm. info comes from the
// manifest — the guest is never trusted for identity — and shape must match
// the canvas the shell will hand to Draw. The returned game implements
// io.Closer; wrap it in engine.Safe like any other game.
func (r *Runtime) Load(key string, wasmBytes []byte, info sdk.Info, shape sdk.CellShape) (sdk.Game, error) {
	cm, err := r.Compile(key, wasmBytes)
	if err != nil {
		return nil, err
	}

	cfg := wazero.NewModuleConfig().
		WithName("").                      // anonymous: two instances of one game must not collide
		WithStartFunctions("_initialize"). // reactor module, not a command
		WithStdout(io.Discard).
		WithStderr(io.Discard).
		WithRandSource(rand.Reader).
		WithSysWalltime().
		WithSysNanotime()
		// Deliberately absent: args, env, preopened directories. The sandbox
		// has no way to reach the host filesystem or network.

	ctx, cancel := context.WithTimeout(r.ctx, initTimeout)
	defer cancel()
	mod, err := r.rt.InstantiateModule(ctx, cm, cfg)
	if err != nil {
		return nil, fmt.Errorf("instantiating wasm: %w", err)
	}

	g := &wasmGame{ctx: r.ctx, mod: mod, info: info}
	if err := g.start(shape); err != nil {
		mod.Close(r.ctx)
		return nil, err
	}
	return g, nil
}

// fn fetches a required export; Compile has already verified it exists.
func mustFn(mod api.Module, name string) api.Function {
	f := mod.ExportedFunction(name)
	if f == nil {
		panic("plugin: missing export " + name)
	}
	return f
}
