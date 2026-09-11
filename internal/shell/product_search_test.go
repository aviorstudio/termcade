package shell

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/charmbracelet/x/ansi"
)

func TestProductSearchFields(t *testing.T) {
	g := registry.Game{ID: "owner/slug", Name: "Puzzle", Description: "Falling blocks"}
	for _, q := range []string{"", "  ", " PUZZLE ", "slu", "BLOCKS"} {
		if !matchesProductSearch(g, q) {
			t.Fatalf("did not match %q", q)
		}
	}
	for _, q := range []string{"owner", "missing", "puzzle falling"} {
		if matchesProductSearch(g, q) {
			t.Fatalf("unexpected match %q", q)
		}
	}
}

func TestProductSearchTypingAndStaleReplies(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	m, _ = step(t, m, productKey("/"))
	if !m.app.searchFocus {
		t.Fatal("/ did not focus search")
	}
	base := len(f.calls)
	m, first := step(t, m, productKey("r"))
	old := m.app.search.gen
	m, last := step(t, m, productKey("e"))
	if first == nil || last == nil || len(f.calls) != base {
		t.Fatal("typing fetched early")
	}
	for _, item := range m.productItems() {
		if item.Kind == "game" {
			t.Fatal("typing exposed stale game actions")
		}
	}
	m, cmd := step(t, m, productSearchTick{old})
	if cmd != nil {
		t.Fatal("stale debounce triggered request")
	}
	m = drain(t, m, last)
	if len(f.calls) != base+1 || f.calls[base].Value != "re" {
		t.Fatalf("requests: %#v", f.calls[base:])
	}
	m, _ = step(t, m, productSearchMsg{old, []registry.Game{{ID: "owner/stale"}}, errors.New("stale")})
	if m.app.search.err != "" || len(m.productItems()) != 4 {
		t.Fatalf("stale reply changed results: %#v", m.productItems())
	}
	m, _ = step(t, m, productKey("enter"))
	if m.app.searchFocus || m.app.loc.Page != "marketplace" {
		t.Fatal("Enter activated a result while editing")
	}
	m, cmd = step(t, m, productKey("esc"))
	m = drain(t, m, cmd)
	if m.app.marketQuery != "re" || len(m.productItems()) != 4 {
		t.Fatal("root-page Escape lost the remembered search results")
	}
	m, _ = step(t, m, productKey("/"))
	m, _ = step(t, m, productKey("esc"))
	if m.app.marketQuery != "re" || m.app.loc.Page != "marketplace" {
		t.Fatal("Esc cleared query or navigated")
	}
	m, _ = step(t, m, productKey("/"))
	m, _ = step(t, m, productKey("ctrl+n"))
	if m.app.searchFocus || m.app.focus != 0 {
		t.Fatal("Ctrl+N did not open navigation")
	}
}

func TestProductMarketplaceSearchDoesNotChangeNavGames(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	before := len(m.productNavItems())
	m, _ = step(t, m, productKey("/"))
	m, cmd := step(t, m, tea.PasteMsg{Content: " LOCAL\n"})
	m = drain(t, m, cmd)
	if !m.inAccount("acme/remote") || len(m.productNavItems()) != before {
		t.Fatal("search changed membership/sidebar")
	}
	found := false
	for _, item := range m.productNavItems() {
		if item.Kind == "game" && item.ID == "acme/local" {
			found = true
		}
	}
	if !found {
		t.Fatal("local library game missing from navigation")
	}
}

func TestProductSearchKeepsIndentWhenSelected(t *testing.T) {
	m, _ := newProductFixture(t)
	m = drain(t, m, m.Init())
	idle := ansi.Strip(m.productSearchView(80))
	m, _ = step(t, m, productKey("/"))
	focused := ansi.Strip(m.productSearchView(80))
	column := func(line string) int {
		i := strings.Index(line, "Search")
		if i < 0 {
			return -1
		}
		return ansi.StringWidth(line[:i])
	}
	if got, want := column(focused), column(idle); got < 0 || got != want {
		t.Fatalf("search shifted left when selected:\nidle %q\nfocused %q", idle, focused)
	}
}

func TestProductSearchValidationAndFooterFit(t *testing.T) {
	m, f := newProductFixture(t)
	m = drain(t, m, m.Init())
	before := len(f.calls)
	m, _ = step(t, m, productKey("/"))
	m, cmd := step(t, m, tea.PasteMsg{Content: strings.Repeat("é", 33)})
	if cmd != nil || len(f.calls) != before || !strings.Contains(m.app.search.err, "64 UTF-8 bytes") {
		t.Fatal("invalid search issued a request")
	}
	for _, w := range []int{40, 80, 120} {
		view := ansi.Strip(m.productPageView(w, 24))
		if len(strings.Split(view, "\n")) > 24 || !strings.Contains(view, "Ctrl+U clear") {
			t.Fatal("search displaced footer")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > w {
				t.Fatal("search overflowed width")
			}
		}
	}
}
