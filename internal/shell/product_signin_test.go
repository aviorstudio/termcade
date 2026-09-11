package shell

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/charmbracelet/x/ansi"
)

func TestProductSignInFromNavIsModalAndCancelRejectsLateApproval(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.SignedIn = false
	m = drain(t, m, m.Init())
	m.app.focus = 0
	m.app.nav = len(m.productNavItems()) - 1
	loc, history, nav := m.app.loc, len(m.app.history), m.app.nav
	m, start := step(t, m, productKey("enter"))
	if m.app.signIn == nil || m.app.loc != loc || len(m.app.history) != history {
		t.Fatal("sign-in replaced the underlying page")
	}
	m, poll := step(t, m, start())
	if poll == nil || m.app.pairingURL == "" {
		t.Fatal("pairing did not start in dialog")
	}
	gen, ctx := m.app.gen, m.app.ctx
	m, open := step(t, m, productKey("enter"))
	m = drain(t, m, open)
	if m.app.gen != gen || ctx.Err() != nil || !m.app.loading {
		t.Fatal("explicit browser opening canceled device polling")
	}
	if f.calls[len(f.calls)-1].Kind != "open-url" {
		t.Fatal("Open browser was not actionable")
	}
	for _, key := range []string{"tab", "shift+tab", "ctrl+n", "x", "/", "pgdown"} {
		m, _ = step(t, m, productKey(key))
	}
	m, _ = step(t, m, tea.PasteMsg{Content: "must-not-enter-background-search"})
	if m.app.nav != nav || m.app.focus != 0 || m.app.marketQuery != "" || m.app.loc != loc {
		t.Fatal("dialog input escaped into the background")
	}
	if m.app.signIn == nil || m.app.signIn.button != 0 {
		t.Fatal("Shift+Tab did not reverse Tab, or Ctrl+N dismissed the dialog")
	}
	m, close := step(t, m, productKey("esc"))
	if close != nil || m.app.signIn != nil || ctx.Err() == nil || m.app.loc != loc || len(m.app.history) != history || m.app.focus != 0 {
		t.Fatal("Escape did not cancel polling and return to the unchanged background")
	}
	m, cmd := step(t, m, productMsg{Gen: gen, Request: ProductRequest{Kind: "pair-wait"}, Reply: ProductReply{Session: registry.Session{Token: "must-not-render"}}})
	if cmd != nil || strings.Contains(view(m), "must-not-render") {
		t.Fatal("dismissed dialog accepted late approval")
	}
}

func TestProductSignInDialogSuccessRestoresRouteAndSearch(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	m.app.marketQuery = "remote"
	m.app.search = productSearchState{active: true, games: []registry.Game{f.snapshot.Catalog[1]}}
	m.app.selection, m.app.focus = 0, 1
	loc, history := m.app.loc, len(m.app.history)
	m, start := m.activateProduct("sign-in", "")
	m = drain(t, m, start)
	if m.app.signIn != nil || m.app.loc != loc || len(m.app.history) != history || m.app.focus != 1 {
		t.Fatal("successful login did not return to its originating screen")
	}
	if m.app.marketQuery != "remote" || len(m.productItems()) != 2 || m.productItems()[1].ID != "acme/remote" {
		t.Fatal("dialog lost existing search results")
	}
	if m.app.round.UserCode != "" || m.app.pairingURL != "" {
		t.Fatal("completed pairing left its code visible")
	}
}

func TestProductSignInDialogCancelDuringSaveReconciles(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.SignedIn = false
	m = drain(t, m, m.Init())
	m, start := m.activateProduct("sign-in", "")
	m, poll := step(t, m, start())
	m, save := step(t, m, poll())
	if save == nil || !m.app.signIn.saving {
		t.Fatal("approved pairing did not schedule saving")
	}
	// Control a save that completed, but whose result has not reached the model.
	f.snapshot.SignedIn = true
	late := save()
	m, reconcile := step(t, m, productKey("esc"))
	if reconcile == nil || m.app.signIn != nil {
		t.Fatal("cancel during saving did not schedule reconciliation")
	}
	m, ignored := step(t, m, late)
	if ignored != nil || m.app.snapshot.SignedIn {
		t.Fatal("late save result was accepted")
	}
	m = drain(t, m, reconcile)
	if !m.app.snapshot.SignedIn || m.app.loc.Page != "marketplace" {
		t.Fatal("committed credentials were not reconciled on the original page")
	}
}

func TestProductSignInDialogFitsAndKeepsControlsVisible(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, start := m.activateProduct("sign-in", "")
	m, _ = step(t, m, start())
	defer m.cancelProduct()
	for _, size := range [][2]int{{40, 20}, {80, 24}, {120, 40}, {32, 12}} {
		m.termW, m.termH = size[0], size[1]
		rendered := ansi.Strip(view(m))
		lines := strings.Split(rendered, "\n")
		if len(lines) > size[1] {
			t.Fatal("dialog exceeded terminal height")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("dialog exceeded terminal width")
			}
		}
		if size[0] >= 40 {
			raw := view(m)
			if !strings.Contains(raw, "\x1b]8;;http://127.0.0.1:8081/pair/ABCD-EFGH\x1b\\") {
				t.Fatal("pairing URL is not a hyperlink that includes the user code")
			}
			for _, text := range []string{"Sign in", "ABCD-EFGH", "127.0.0.1:8081/pair", "[Open browser]", "[Cancel]"} {
				if !strings.Contains(rendered, text) {
					t.Fatalf("dialog at %v omitted %q", size, text)
				}
			}
			for _, extra := range []string{"SIGN IN", "Approve this terminal", "Pairing code:", "[New code]", "Waiting for browser approval"} {
				if strings.Contains(rendered, extra) {
					t.Fatalf("dialog kept instructional copy %q", extra)
				}
			}
			width, height := min(72, m.termW-2), min(20, m.termH-2)
			x, y := (m.termW-width)/2, (m.termH-height)/2
			var panel []string
			divider := -1
			for i, line := range lines[y : y+height] {
				row := ansi.Cut(line, x, x+width)
				panel = append(panel, row)
				if row == "│ "+strings.Repeat("─", width-4)+" │" {
					divider = i
				}
			}
			if divider < 0 {
				t.Fatal("dialog divider missing")
			}
			above, below := strings.Join(panel[:divider], "\n"), strings.Join(panel[divider+1:], "\n")
			if !strings.Contains(panel[1], "Sign in") {
				t.Fatalf("Sign in title is not at the top of the dialog: %q", panel[1])
			}
			urlAt, optionAt := strings.Index(above, "127.0.0.1:8081/pair"), strings.Index(above, "[Open browser]")
			codeAt := strings.LastIndex(above, "ABCD-EFGH")
			if urlAt < 0 || codeAt <= urlAt || optionAt <= codeAt {
				t.Fatal("dialog body is not URL, then code, then options")
			}
			urlLine, codeLine := strings.Count(above[:urlAt], "\n"), strings.Count(above[:codeAt], "\n")
			if codeLine != urlLine+2 {
				t.Fatal("missing blank line between the pairing URL and code")
			}
			for _, option := range []string{"[Open browser]", "[Cancel]"} {
				if !strings.Contains(above, option) || strings.Contains(below, option) {
					t.Fatalf("%q must be above the divider", option)
				}
			}
			if strings.TrimSpace(below) != "" && strings.Contains(below, "↓ Tab") {
				t.Fatal("global Tab/Enter/Esc instructions should be implied")
			}
			assertCenteredIn(t, above, "127.0.0.1:8081/pair")
			var codeRow string
			for _, line := range strings.Split(above, "\n") {
				if strings.Contains(line, "ABCD-EFGH") && !strings.Contains(line, "pair") {
					codeRow = line
					break
				}
			}
			assertCenteredIn(t, codeRow, "ABCD-EFGH")
			if strings.Contains(above, "[Open browser] [Cancel]") {
				assertCenteredIn(t, above, "[Open browser] [Cancel]")
			} else {
				assertCenteredIn(t, above, "[Open browser]")
			}
		} else {
			if !strings.Contains(rendered, "Resize") {
				t.Fatal("tiny terminal did not show safe fallback")
			}
			_, cmd := step(t, m, productKey("enter"))
			if cmd != nil {
				t.Fatal("invisible button remained actionable")
			}
		}
	}
}

func TestProductSignInDialogDoesNotClosePausedGame(t *testing.T) {
	m, f := newProductFixture(t)
	f.snapshot.SignedIn = false
	m = drain(t, m, m.Init())
	m, _ = m.activateProduct("play", "acme/local")
	m, _ = step(t, m, productKey("esc"))
	if m.screen != screenPaused {
		t.Fatal("control: game did not pause")
	}
	m.app.gameNav = true
	m.app.nav = len(m.productNavItems()) - 1
	game := m.game
	m, _ = step(t, m, productKey("enter"))
	if m.app.signIn == nil || m.game != game || m.screen != screenPaused {
		t.Fatal("sign-in closed or resumed the paused game")
	}
	m, _ = step(t, m, productKey("esc"))
	if m.app.signIn != nil || m.game != game || m.screen != screenPaused || !m.app.gameNav {
		t.Fatal("dialog dismissal changed paused gameplay")
	}
	m = m.closeProductGame()
}

func assertCenteredIn(t *testing.T, panel, needle string) {
	t.Helper()
	for _, line := range strings.Split(panel, "\n") {
		idx := strings.Index(line, needle)
		if idx < 0 {
			continue
		}
		left, right := ansi.StringWidth(line[:idx]), ansi.StringWidth(line[idx+len(needle):])
		if min(left, right)*2+2 < max(left, right) {
			t.Fatalf("%q is not centered: left=%d right=%d in %q", needle, left, right, line)
		}
		return
	}
	t.Fatalf("%q missing from dialog body", needle)
}
