package shell

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/aviorstudio/termcade/sdk"
)

var (
	hudDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8a8a"))
	hudAccent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f2f2f2"))
)

// renderHUD styles a game's structured status line. All text is sanitized
// first: games (installed ones especially) are untrusted, and a raw escape
// sequence in a HUD string could corrupt the whole frame.
func renderHUD(h sdk.HUD) string {
	parts := make([]string, 0, len(h.Fields)+1)
	for _, f := range h.Fields {
		text := strings.TrimSpace(sanitize(f.Label) + " " + sanitize(f.Value))
		if text == "" {
			continue
		}
		if f.Accent {
			parts = append(parts, hudAccent.Render(text))
		} else {
			parts = append(parts, hudDim.Render(text))
		}
	}
	if h.Hint != "" {
		parts = append(parts, hudDim.Render(sanitize(h.Hint)))
	}
	return strings.Join(parts, "   ")
}

// sanitize drops C0/C1 control characters and DEL, which covers ESC and with
// it every ANSI sequence introducer.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
