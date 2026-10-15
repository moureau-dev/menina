package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const inputHelp = "enter confirm · shift+tab/esc cancel · ctrl+c exit"

// dockBottom pads content with blank lines so it sits at the bottom of a
// terminal of the given height.
func dockBottom(height int, content string) string {
	if height <= 0 {
		return content
	}
	if pad := height - lipgloss.Height(content); pad > 0 {
		return strings.Repeat("\n", pad) + content
	}
	return content
}

// Action reports how a widget finished.
type Action int

const (
	// ActionSelected means the user chose an item / submitted the form.
	ActionSelected Action = iota
	// ActionBack means the user asked to go back (Esc / Shift+Tab).
	ActionBack
	// ActionQuit means the user aborted the whole program (Ctrl+C twice).
	ActionQuit
)

// helpLine renders the key-hint footer, wrapping to the terminal width so the
// whole hint stays visible on narrow terminals instead of being truncated.
func helpLine(width int, text string) string {
	if width > 0 {
		return helpStyle.Width(width).Render(text)
	}
	return helpStyle.Render(text)
}
