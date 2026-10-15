package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type inputModel struct {
	title  string
	input  textinput.Model
	width  int
	height int
	action Action
	done   bool
	armed  bool // first Ctrl+C pressed, waiting for the second
}

func newInputModel(title, def string) inputModel {
	ti := textinput.New()
	ti.Placeholder = def
	ti.SetValue(def)
	ti.CursorEnd()
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 60
	return inputModel{title: title, input: ti, action: ActionQuit}
}

func (m inputModel) Init() tea.Cmd { return textinput.Blink }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			if m.armed {
				m.action = ActionQuit
				m.done = true
				return m, tea.Quit
			}
			m.armed = true
			return m, nil
		}
		m.armed = false
		switch msg.String() {
		case "enter":
			m.action = ActionSelected
			m.done = true
			return m, tea.Quit
		case "esc", "shift+tab":
			m.action = ActionBack
			m.done = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m inputModel) View() string {
	if m.done {
		return ""
	}
	help := inputHelp
	if m.armed {
		help = "press ctrl+c again to exit"
	}
	body := titleStyle.Render(m.title) + "\n\n" + m.input.View() + "\n\n" + helpLine(m.width, help) + "\n"
	if m.height > 0 {
		return dockBottom(m.height, body)
	}
	return "\n" + body
}

// Input prompts for a single line of text, docked at the bottom of the
// terminal. def pre-fills the field and is shown as the placeholder. It returns
// the submitted value and how the widget finished.
func Input(title, def string) (string, Action) {
	m := newInputModel(title, def)
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return def, ActionQuit
	}
	fm, ok := res.(inputModel)
	if !ok {
		return def, ActionQuit
	}
	return fm.input.Value(), fm.action
}

// Confirm asks a yes/no question, rendered as a select list. It returns the
// answer and how the widget finished.
func Confirm(label string, def bool) (bool, Action) {
	d := 1
	if def {
		d = 0
	}
	idx, action := Select(label, []Option{{Label: "Yes"}, {Label: "No"}}, d, false)
	if action != ActionSelected {
		return def, action
	}
	return idx == 0, ActionSelected
}
