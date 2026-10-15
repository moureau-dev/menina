package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInputModelEnterReturnsValue(t *testing.T) {
	m := newInputModel("Name", "")
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("api")})
	m = nm.(inputModel)
	if m.input.Value() != "api" {
		t.Fatalf("value = %q, want api", m.input.Value())
	}
	if m.action == ActionBack {
		t.Fatal("should not be canceled")
	}
}

func TestInputModelPrefillsDefault(t *testing.T) {
	m := newInputModel("Registry domain", "registry.test")
	if m.input.Value() != "registry.test" {
		t.Fatalf("value = %q, want prefilled default", m.input.Value())
	}
}

func TestInputModelEscCancels(t *testing.T) {
	m := newInputModel("Name", "")
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(inputModel)
	if m.action != ActionBack {
		t.Fatalf("esc action = %v, want back", m.action)
	}
}
