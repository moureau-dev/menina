// Package tui provides Bubble Tea interactive widgets for the CLI. It is only
// used when stdin is a real terminal; scripts and tests use the plain
// line-based prompts instead.
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Option is a selectable choice with an optional description.
type Option struct {
	Label       string
	Description string
}

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			Background(lipgloss.Color("#7D56F4")).
			Bold(true).
			Padding(0, 1)
	helpStyle   = lipgloss.NewStyle().Faint(true)
	itemStyles  = list.NewDefaultItemStyles()
	filterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	blockStyle  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)
	blockTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("170"))
	dirStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	badgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

const (
	maxDescLines = 3
	maxVisible   = 6
)

type selectModel struct {
	title     string
	badge     string
	options   []Option
	filtered  []int // indices into options matching the current filter
	cursor    int   // position within filtered
	offset    int   // first visible row within filtered
	filter    string
	filtering bool
	canGoBack bool
	prelude   string
	width     int
	height    int
	choice    int
	action    Action
	armed     bool // first Ctrl+C pressed, waiting for the second
	done      bool
}

func newSelectModel(title string, options []Option, def int, canGoBack bool, prelude string) selectModel {
	m := selectModel{title: title, options: options, canGoBack: canGoBack, prelude: prelude, action: ActionQuit}
	m.filtered = make([]int, len(options))
	for i := range options {
		m.filtered[i] = i
	}
	if def >= 0 && def < len(options) {
		m.cursor = def
	}
	m.choice = def
	return m
}

func (m selectModel) Init() tea.Cmd {
	if m.prelude != "" {
		return tea.Println(m.prelude)
	}
	return nil
}

func (m *selectModel) applyFilter() {
	m.filtered = m.filtered[:0]
	f := strings.ToLower(m.filter)
	for i, o := range m.options {
		if f == "" || strings.Contains(strings.ToLower(o.Label), f) {
			m.filtered = append(m.filtered, i)
		}
	}
	m.cursor, m.offset = 0, 0
}

func (m selectModel) contentWidth() int {
	w := m.width - 4
	if w < 10 {
		w = 10
	}
	return w
}

func (m selectModel) descLines(o Option) []string {
	if o.Description == "" {
		return nil
	}
	wrapped := lipgloss.NewStyle().Width(m.contentWidth()).Render(o.Description)
	lines := strings.Split(wrapped, "\n")
	if len(lines) > maxDescLines {
		lines = lines[:maxDescLines]
		lines[maxDescLines-1] = ansi.Truncate(lines[maxDescLines-1], m.contentWidth(), "…")
	}
	return lines
}

func (m selectModel) itemHeight(i int) int {
	return 1 + len(m.descLines(m.options[m.filtered[i]])) + 1 // title + desc + spacing
}

// preludeHeight is how many terminal lines the output block printed above the
// menu occupies (0 when there is none).
func (m selectModel) preludeHeight() int {
	if m.prelude == "" {
		return 0
	}
	return lipgloss.Height(m.prelude)
}

// availableLines is the number of lines the item area may use (title + footer
// + a bottom margin + the output block above are reserved).
func (m selectModel) availableLines() int {
	h := m.height
	if h <= 0 {
		h = 24
	}
	n := h - m.preludeHeight() - 3
	if n < 3 {
		n = 3
	}
	return n
}

func (m selectModel) heightFrom(a, b int) int {
	total := 0
	for i := a; i < b && i < len(m.filtered); i++ {
		total += m.itemHeight(i)
	}
	return total
}

// clamp keeps the cursor visible, scrolling one row at a time.
func (m *selectModel) clamp() {
	if len(m.filtered) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.offset > m.cursor {
		m.offset = m.cursor
	}
	if m.cursor-m.offset >= maxVisible {
		m.offset = m.cursor - maxVisible + 1
	}
	maxLines := m.availableLines()
	for m.offset < m.cursor && m.heightFrom(m.offset, m.cursor+1) > maxLines {
		m.offset++
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *selectModel) move(delta int) {
	m.armed = false
	m.cursor += delta
	m.clamp()
}

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m selectModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C must be pressed twice to abort.
	if msg.Type == tea.KeyCtrlC {
		if m.armed {
			m.action = ActionQuit
			m.done = true
			return m, tea.Quit
		}
		m.armed = true
		return m, nil
	}

	if m.filtering {
		switch msg.Type {
		case tea.KeyRunes:
			m.filter += string(msg.Runes)
			m.applyFilter()
		case tea.KeySpace:
			m.filter += " "
			m.applyFilter()
		case tea.KeyBackspace:
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.applyFilter()
			}
		case tea.KeyEnter:
			m.filtering = false
		case tea.KeyEsc:
			m.filter, m.filtering = "", false
			m.applyFilter()
		}
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "/":
		m.filtering = true
	case "enter":
		if len(m.filtered) > 0 {
			m.choice = m.filtered[m.cursor]
		}
		m.action = ActionSelected
		m.done = true
		return m, tea.Quit
	case "esc", "shift+tab":
		m.action = ActionBack
		m.done = true
		return m, tea.Quit
	default:
		m.armed = false
	}
	return m, nil
}

func (m selectModel) header() string {
	title := titleStyle.Render(m.title)
	if m.badge != "" {
		title += " " + badgeStyle.Render(m.badge)
	}
	return title
}

func (m selectModel) footer() string {
	if m.armed {
		return helpStyle.Render("press ctrl+c again to exit")
	}
	keys := "↑/↓ move · enter select · / filter · ctrl+c exit"
	if m.canGoBack {
		keys = "↑/↓ move · enter select · / filter · shift+tab/esc back · ctrl+c exit"
	}
	return helpLine(m.width, keys)
}

func (m selectModel) renderItem(i int) []string {
	o := m.options[m.filtered[i]]
	width := m.contentWidth()
	label := ansi.Truncate(o.Label, width, "…")
	var out []string
	if i == m.cursor {
		out = append(out, itemStyles.SelectedTitle.Render(label))
		for _, dl := range m.descLines(o) {
			out = append(out, itemStyles.SelectedDesc.Render(dl))
		}
	} else {
		out = append(out, itemStyles.NormalTitle.Render(label))
		for _, dl := range m.descLines(o) {
			out = append(out, itemStyles.NormalDesc.Render(dl))
		}
	}
	return append(out, "") // spacing
}

func (m selectModel) View() string {
	if m.done {
		return ""
	}
	lines := []string{m.header(), ""} // blank line under the title
	if m.filtering || m.filter != "" {
		lines = append(lines, filterStyle.Render("/ "+m.filter))
	}
	if len(m.filtered) == 0 {
		lines = append(lines, "  (no matches)")
	}
	maxLines := m.availableLines()
	used := 0
	for i, shown := m.offset, 0; i < len(m.filtered) && shown < maxVisible; i++ {
		h := m.itemHeight(i)
		if used > 0 && used+h > maxLines {
			break
		}
		lines = append(lines, m.renderItem(i)...)
		used += h
		shown++
	}
	lines = append(lines, m.footer(), "") // trailing blank = bottom margin
	content := strings.Join(lines, "\n")
	if m.height <= 0 {
		return content
	}
	return dockBottom(m.height-m.preludeHeight(), content)
}

// Select shows a filterable, scrollable list, docked at the bottom of the
// terminal. It returns the chosen index and how the widget finished:
// ActionSelected, ActionBack (Esc / Shift+Tab) or ActionQuit (Ctrl+C twice).
func Select(title string, options []Option, def int, canGoBack bool) (int, Action) {
	return selectProgram(title, options, def, canGoBack, "")
}

// SelectWithPrelude is Select with a styled block printed above the list (e.g.
// the output of the previous action), so it stays visible on screen.
func SelectWithPrelude(title string, options []Option, def int, canGoBack bool, prelude string) (int, Action) {
	return selectProgram(title, options, def, canGoBack, prelude)
}

// SelectWithPreludeBadge is SelectWithPrelude with a small badge next to the
// title (for example HOST or CLIENT).
func SelectWithPreludeBadge(title, badge string, options []Option, def int, canGoBack bool, prelude string) (int, Action) {
	m := newSelectModel(title, options, def, canGoBack, prelude)
	m.badge = badge
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return def, ActionQuit
	}
	fm, ok := res.(selectModel)
	if !ok {
		return def, ActionQuit
	}
	return fm.choice, fm.action
}

func selectProgram(title string, options []Option, def int, canGoBack bool, prelude string) (int, Action) {
	m := newSelectModel(title, options, def, canGoBack, prelude)
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return def, ActionQuit
	}
	fm, ok := res.(selectModel)
	if !ok {
		return def, ActionQuit
	}
	return fm.choice, fm.action
}

// Block styles command output as a bordered box with a title, so it can be
// printed above the menu instead of as raw text, labelled with the action that
// produced it.
func Block(title, s string) string {
	s = strings.TrimRight(s, "\n")
	body := blockTitle.Render(title)
	if strings.TrimSpace(s) != "" {
		body += "\n" + s
	}
	return blockStyle.Render(body)
}
