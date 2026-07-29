// Package manifest defines the termcade.toml game manifest and the .tcade
// package format (a zip of termcade.toml + game.wasm). The manifest — never
// the wasm — is the authority on a game's identity.
package manifest

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/aviorstudio/termcade/sdk"
)

// FileName is the manifest's name inside a package and an install dir.
const FileName = "termcade.toml"

// WasmName is the module's name inside a package and an install dir.
const WasmName = "game.wasm"

type Manifest struct {
	Game         Game              `toml:"game"`
	Requirements Requirements      `toml:"requirements"`
	Controls     map[string]string `toml:"controls"`
}

type Game struct {
	ID          string `toml:"id"`   // "<author>/<slug>", globally unique
	Name        string `toml:"name"` // display title
	Version     string `toml:"version"`
	Description string `toml:"description"`
}

type Requirements struct {
	ABI    int `toml:"abi"`
	Width  int `toml:"width"` // logical playfield, must match termcade_playfield
	Height int `toml:"height"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)

// Limits on manifest content. Manifests come from strangers: strings end up
// in the menu, so they are length-capped and must be printable (no control
// characters — that is what keeps ANSI escapes out of the arcade's frames),
// and the playfield is bounded because the HOST allocates the canvas from
// these numbers before any sandboxing applies.
const (
	MaxPlayfieldW = 200
	MaxPlayfieldH = 120

	maxIDLen      = 64
	maxNameLen    = 32
	maxVersionLen = 32
	maxDescLen    = 200
	maxControls   = 12
	maxControlLen = 48
)

// Parse decodes and validates a termcade.toml.
func Parse(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	switch {
	case m.Game.ID == "":
		return fmt.Errorf("game.id is required")
	case len(m.Game.ID) > maxIDLen:
		return fmt.Errorf("game.id exceeds %d characters", maxIDLen)
	case !idRe.MatchString(m.Game.ID):
		return fmt.Errorf("game.id %q must be author/slug, lowercase [a-z0-9-]", m.Game.ID)
	case m.Game.Name == "":
		return fmt.Errorf("game.name is required")
	case m.Game.Version == "":
		return fmt.Errorf("game.version is required")
	case m.Requirements.ABI < 1:
		return fmt.Errorf("requirements.abi is required")
	case m.Requirements.Width < 1 || m.Requirements.Height < 1:
		return fmt.Errorf("requirements.width/height are required")
	case m.Requirements.Width > MaxPlayfieldW || m.Requirements.Height > MaxPlayfieldH:
		return fmt.Errorf("playfield exceeds the %dx%d maximum", MaxPlayfieldW, MaxPlayfieldH)
	case m.Requirements.Height%2 != 0:
		return fmt.Errorf("requirements.height must be even (a cell spans two units)")
	case len(m.Controls) > maxControls:
		return fmt.Errorf("more than %d controls entries", maxControls)
	}
	if err := checkText("game.name", m.Game.Name, maxNameLen); err != nil {
		return err
	}
	if err := checkText("game.version", m.Game.Version, maxVersionLen); err != nil {
		return err
	}
	if err := checkText("game.description", m.Game.Description, maxDescLen); err != nil {
		return err
	}
	for k, v := range m.Controls {
		if err := checkText("controls key", k, maxControlLen); err != nil {
			return err
		}
		if err := checkText("controls value", v, maxControlLen); err != nil {
			return err
		}
	}
	return nil
}

// checkText enforces the rules for display strings: printable (no control
// characters, so no escape sequences) and length-capped.
func checkText(field, s string, maxRunes int) error {
	if utf8.RuneCountInString(s) > maxRunes {
		return fmt.Errorf("%s exceeds %d characters", field, maxRunes)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}

// Author and Slug split the namespaced id.
func (m *Manifest) Author() string { return strings.SplitN(m.Game.ID, "/", 2)[0] }
func (m *Manifest) Slug() string   { return strings.SplitN(m.Game.ID, "/", 2)[1] }

// Info is the sdk view of the manifest, as handed to the shell.
func (m *Manifest) Info() sdk.Info {
	return sdk.Info{
		ID:     m.Game.ID,
		Title:  strings.ToUpper(m.Game.Name),
		PixelW: m.Requirements.Width,
		PixelH: m.Requirements.Height,
	}
}

// CompatibleABI reports whether this termcade can run the game.
func (m *Manifest) CompatibleABI() bool { return m.Requirements.ABI == sdk.ABIVersion }
