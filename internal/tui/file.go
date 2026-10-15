package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type fileEntry struct {
	name  string
	path  string
	isDir bool
}

type fileModel struct {
	title   string
	dir     string
	entries []fileEntry
	cursor  int
	offset  int
	width   int
	height  int
	chosen  string
	action  Action
	armed   bool
	done    bool
	err     string
}

func isArchive(name string) bool {
	l := strings.ToLower(name)
	for _, s := range []string{".tar.gz", ".tgz", ".tar", ".gz", ".zip", ".bz2", ".xz", ".zst"} {
		if strings.HasSuffix(l, s) {
			return true
		}
	}
	return false
}

func newFileModel(title, dir string) fileModel {
	m := fileModel{title: title, dir: dir, action: ActionQuit}
	m.load()
	return m
}

func (m *fileModel) load() {
	m.entries = nil
	m.cursor, m.offset = 0, 0
	m.entries = append(m.entries, fileEntry{name: "..", path: filepath.Dir(m.dir), isDir: true})
	des, err := os.ReadDir(m.dir)
	if err != nil {
		m.err = err.Error()
		return
	}
	m.err = ""
	var dirs, files []fileEntry
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		p := filepath.Join(m.dir, de.Name())
		if de.IsDir() {
			dirs = append(dirs, fileEntry{name: de.Name() + "/", path: p, isDir: true})
		} else if isArchive(de.Name()) {
			files = append(files, fileEntry{name: de.Name(), path: p})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	m.entries = append(m.entries, dirs...)
	m.entries = append(m.entries, files...)
}

func (m fileModel) Init() tea.Cmd { return nil }

func (m *fileModel) clamp() {
	if len(m.entries) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
	if m.offset > m.cursor {
		m.offset = m.cursor
	}
	if m.cursor-m.offset >= maxVisible {
		m.offset = m.cursor - maxVisible + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m fileModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
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
		case "up", "k":
			m.cursor--
			m.clamp()
		case "down", "j":
			m.cursor++
			m.clamp()
		case "enter":
			if len(m.entries) == 0 {
				return m, nil
			}
			e := m.entries[m.cursor]
			if e.isDir {
				m.dir = e.path
				m.load()
				return m, nil
			}
			m.chosen = e.path
			m.action = ActionSelected
			m.done = true
			return m, tea.Quit
		case "esc", "shift+tab":
			m.action = ActionBack
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m fileModel) footer() string {
	if m.armed {
		return helpStyle.Render("press ctrl+c again to exit")
	}
	return helpLine(m.width, "↑/↓ move · enter open/select · shift+tab/esc back · ctrl+c exit")
}

func (m fileModel) View() string {
	if m.done {
		return ""
	}
	lines := []string{titleStyle.Render(m.title), "", filterStyle.Render("  " + m.dir)}
	if m.err != "" {
		lines = append(lines, filterStyle.Render("  "+m.err))
	}
	end := m.offset + maxVisible
	if end > len(m.entries) {
		end = len(m.entries)
	}
	for i := m.offset; i < end; i++ {
		e := m.entries[i]
		switch {
		case i == m.cursor:
			lines = append(lines, itemStyles.SelectedTitle.Render(e.name))
		case e.isDir:
			lines = append(lines, itemStyles.NormalTitle.Render(dirStyle.Render(e.name)))
		default:
			lines = append(lines, itemStyles.NormalTitle.Render(e.name))
		}
		lines = append(lines, "")
	}
	lines = append(lines, m.footer(), "")
	content := strings.Join(lines, "\n")
	if m.height <= 0 {
		return content
	}
	return dockBottom(m.height, content)
}

// PickFile shows a file picker rooted at startDir, listing directories and
// archive files. It returns the chosen path and ok=false when cancelled.
func PickFile(title, startDir string) (string, bool) {
	if startDir == "" {
		startDir, _ = os.Getwd()
	}
	m := newFileModel(title, startDir)
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", false
	}
	fm, ok := res.(fileModel)
	if !ok || fm.action != ActionSelected || fm.chosen == "" {
		return "", false
	}
	return fm.chosen, true
}
