package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func newModel(n int) selectModel {
	opts := make([]Option, n)
	for i := range opts {
		opts[i] = Option{Label: string(rune('a' + i)), Description: "desc"}
	}
	return newSelectModel("Menina", opts, 0, false, "")
}

func press(m selectModel, key tea.KeyType) selectModel {
	nm, _ := m.Update(tea.KeyMsg{Type: key})
	return nm.(selectModel)
}

func TestBadgeAppearsInHeader(t *testing.T) {
	m := newModel(1)
	m.badge = "client"
	header := ansi.Strip(m.header())
	if !strings.Contains(header, "Menina") || !strings.Contains(header, "client") {
		t.Fatalf("header = %q", header)
	}
}

func TestEnterReturnsCursoredIndex(t *testing.T) {
	m := press(newModel(5), tea.KeyDown)
	m = press(m, tea.KeyEnter)
	if m.action != ActionSelected {
		t.Fatalf("action = %v, want selected", m.action)
	}
	if m.choice != 1 {
		t.Fatalf("choice = %d, want 1", m.choice)
	}
}

func TestEscAndShiftTabAreBack(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyShiftTab} {
		m := press(newModel(3), key)
		if m.action != ActionBack {
			t.Fatalf("%v action = %v, want back", key, m.action)
		}
	}
}

func TestCtrlCRequiresTwoPresses(t *testing.T) {
	m := press(newModel(3), tea.KeyCtrlC)
	if m.done {
		t.Fatal("first ctrl+c should not quit")
	}
	if !m.armed {
		t.Fatal("first ctrl+c should arm the exit")
	}
	m = press(m, tea.KeyCtrlC)
	if m.action != ActionQuit || !m.done {
		t.Fatal("second ctrl+c should quit")
	}
}

func TestDownScrollsOneRow(t *testing.T) {
	m := newModel(20)
	m.height = 10 // two 3-line items fit
	for i := 0; i < 3; i++ {
		m = press(m, tea.KeyDown)
	}
	if m.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", m.cursor)
	}
	// The window must have scrolled one item at a time (not a whole page).
	if m.offset != 2 {
		t.Fatalf("offset = %d, want 2 (smooth scroll)", m.offset)
	}
}

func TestDescriptionWrapsInsteadOfEllipsis(t *testing.T) {
	opts := []Option{{Label: "x", Description: "one two three four five six seven eight nine ten"}}
	m := newSelectModel("t", opts, 0, false, "")
	m.width = 24
	m.height = 40
	lines := m.descLines(m.options[0])
	if len(lines) < 2 {
		t.Fatalf("long description should wrap, got %d line(s): %q", len(lines), lines)
	}
}

func TestViewHasBottomMarginAndClears(t *testing.T) {
	m := newModel(3)
	m.width, m.height = 80, 20
	view := m.View()
	rows := strings.Split(view, "\n")
	if len(rows) != 20 {
		t.Fatalf("view rows = %d, want 20", len(rows))
	}
	if strings.TrimSpace(rows[len(rows)-1]) != "" {
		t.Fatal("last row should be a blank bottom margin")
	}
	m.done = true
	if m.View() != "" {
		t.Fatal("finished widget should render an empty view")
	}
}
