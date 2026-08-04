package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/manifest"
	"github.com/aviorstudio/termcade/sdk"
)

// Games merges builtin games with everything installed under gamesDir:
// builtins first (minus any shadowed by an install), then installed games by
// title, then broken installs, dimmed and unlaunchable. Nothing found on disk
// can prevent the arcade from starting.
func Games(rt *Runtime, gamesDir string, builtins []engine.Registration) []engine.Registration {
	installed, broken := discover(rt, gamesDir)

	shadowed := map[string]bool{}
	for _, reg := range installed {
		shadowed[reg.Info.ID] = true
	}

	out := make([]engine.Registration, 0, len(builtins)+len(installed)+len(broken))
	for _, b := range builtins {
		if !shadowed[b.Info.ID] {
			out = append(out, b)
		}
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Info.Title < installed[j].Info.Title })
	out = append(out, installed...)
	sort.Slice(broken, func(i, j int) bool { return broken[i].Info.Title < broken[j].Info.Title })
	return append(out, broken...)
}

// discover walks <gamesDir>/<author>/<slug>/termcade.toml. Every failure mode
// yields a broken entry rather than an error: the menu is the error surface.
func discover(rt *Runtime, gamesDir string) (ok, broken []engine.Registration) {
	matches, _ := filepath.Glob(filepath.Join(gamesDir, "*", "*", manifest.FileName))
	for _, mPath := range matches {
		dir := filepath.Dir(mPath)
		name := filepath.Base(filepath.Dir(dir)) + "/" + filepath.Base(dir)

		reg, err := load(rt, dir)
		if err != nil {
			broken = append(broken, engine.Registration{
				Info:      sdk.Info{ID: name, Title: name},
				Err:       err,
				Installed: true,
			})
			continue
		}
		reg.Installed = true
		ok = append(ok, reg)
	}
	return ok, broken
}

func load(rt *Runtime, dir string) (engine.Registration, error) {
	raw, err := os.ReadFile(filepath.Join(dir, manifest.FileName))
	if err != nil {
		return engine.Registration{}, err
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return engine.Registration{}, err
	}
	if !m.CompatibleABI() {
		return engine.Registration{}, fmt.Errorf(
			"needs ABI v%d; this termcade speaks v%d", m.Requirements.ABI, sdk.ABIVersion)
	}
	wasmPath := filepath.Join(dir, manifest.WasmName)
	if _, err := os.Stat(wasmPath); err != nil {
		return engine.Registration{}, fmt.Errorf("missing %s", manifest.WasmName)
	}

	info := m.Info()
	return engine.Registration{
		Info:    info,
		Version: m.Game.Version,
		New: func(shape sdk.CellShape) (sdk.Game, error) {
			wasm, err := os.ReadFile(wasmPath)
			if err != nil {
				return nil, err
			}
			return rt.Load(info.ID, wasm, info, shape)
		},
	}, nil
}
