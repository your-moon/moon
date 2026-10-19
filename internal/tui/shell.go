package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Tab is one page of the dashboard. A new tab (run units, PRs, tick log) implements this
// and is added to the slice given to NewShell; the shell does not change.
type Tab interface {
	Title() string
	Init() tea.Cmd
	Update(msg tea.Msg) (Tab, tea.Cmd)
	// View renders exactly the size given by the last tea.WindowSizeMsg.
	View() string
	// Keys is a one-line key hint for the help line.
	Keys() string
	// Capturing reports that the tab is reading typed text, so 1-9 and [ ] must reach it.
	Capturing() bool
}

// scopedTab is a tab that follows the Issues tab's repo scope.
type scopedTab interface {
	SetScope(alias, text string) Tab
}

// Shell is the dashboard: a tab bar over the active tab.
type Shell struct {
	tabs   []Tab
	active int
	w, h   int
	err    string // when set, no tabs: show this and wait for q
}

// NewShell builds a dashboard on the given tab.
func NewShell(tabs []Tab, active int) Shell {
	if active < 0 || active >= len(tabs) {
		active = 0
	}
	return Shell{tabs: tabs, active: active}.syncScope()
}

// ErrorShell shows a message (for example, no config found) instead of tabs.
func ErrorShell(msg string) Shell { return Shell{err: msg} }

func (s Shell) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range s.tabs {
		cmds = append(cmds, t.Init())
	}
	return tea.Batch(cmds...)
}

func (s Shell) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s.err != "" {
		if k, ok := msg.(tea.KeyMsg); ok && (k.String() == "q" || k.String() == "ctrl+c" || k.String() == "esc") {
			return s, tea.Quit
		}
		if w, ok := msg.(tea.WindowSizeMsg); ok {
			s.w, s.h = w.Width, w.Height
		}
		return s, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
		var cmds []tea.Cmd
		for i, t := range s.tabs {
			var c tea.Cmd
			s.tabs[i], c = t.Update(tea.WindowSizeMsg{Width: msg.Width, Height: msg.Height - 1})
			cmds = append(cmds, c)
		}
		return s, tea.Batch(cmds...)
	case tea.KeyMsg:
		if !s.tabs[s.active].Capturing() {
			if n := s.tabKey(msg.String()); n >= 0 {
				s.active = n
				return s, nil
			}
			// a and tab change the repo scope from any tab: the Issues tab owns the logic, the others follow
			if k := msg.String(); (k == "a" || k == "tab") && s.issues() >= 0 && s.issues() != s.active {
				if _, ok := s.tabs[s.active].(scopedTab); ok {
					return s.forwardTo(s.issues(), msg)
				}
			}
		}
	case drillMsg:
		for i, t := range s.tabs {
			if t.Title() == "Issues" {
				s.active = i
			}
		}
	case tea.MouseMsg:
		if msg.Y < 1 {
			return s, nil
		}
		msg.Y--
		return s.forward(msg)
	}
	return s.forward(msg)
}

func (s Shell) forward(msg tea.Msg) (tea.Model, tea.Cmd) { return s.forwardTo(s.active, msg) }

func (s Shell) forwardTo(i int, msg tea.Msg) (tea.Model, tea.Cmd) {
	before, _ := s.scope()
	tabs := append([]Tab(nil), s.tabs...) // value semantics: do not mutate the caller's slice
	var c tea.Cmd
	tabs[i], c = tabs[i].Update(msg)
	s.tabs = tabs
	if _, drill := msg.(drillMsg); i == s.issues() && !drill { // a drill-in narrows the Issues list only
		if after, _ := s.scope(); after != before {
			s = s.syncScope()
		}
	}
	return s, c
}

// issues is the index of the Issues tab, or -1.
func (s Shell) issues() int {
	for i, t := range s.tabs {
		if _, ok := t.(*issuesTab); ok {
			return i
		}
	}
	return -1
}

// scope is the Issues tab's repo filter and its footer text.
func (s Shell) scope() (alias, text string) {
	if i := s.issues(); i >= 0 {
		m := s.tabs[i].(*issuesTab).m
		return m.fAlias, m.scopeText()
	}
	return "", ""
}

// syncScope hands the Issues tab's repo scope to every tab that follows it.
func (s Shell) syncScope() Shell {
	alias, text := s.scope()
	tabs := append([]Tab(nil), s.tabs...)
	for i, t := range tabs {
		if st, ok := t.(scopedTab); ok {
			tabs[i] = st.SetScope(alias, text)
		}
	}
	s.tabs = tabs
	return s
}

// tabKey returns the tab index a key selects, or -1.
func (s Shell) tabKey(k string) int {
	n := len(s.tabs)
	switch k {
	case "]":
		return (s.active + 1) % n
	case "[":
		return (s.active + n - 1) % n
	}
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		if i := int(k[0] - '1'); i < n {
			return i
		}
	}
	return -1
}

// Active is the index of the shown tab.
func (s Shell) Active() int { return s.active }

func (s Shell) View() string {
	if s.err != "" {
		return "\n  " + strings.ReplaceAll(sanitize(s.err), "\n", "\n  ") + "\n\n  Press q to quit."
	}
	if s.w <= 0 || s.h <= 0 {
		return ""
	}
	var bar []string
	for i, t := range s.tabs {
		l := strconv.Itoa(i+1) + " " + t.Title()
		if i == s.active {
			bar = append(bar, stSel.Render("["+l+"]"))
		} else {
			bar = append(bar, " "+l+" ")
		}
	}
	return strings.TrimRight(fit(strings.Join(bar, " "), s.w), " ") + "\n" + s.tabs[s.active].View()
}

// issuesTab adapts Model to Tab.
type issuesTab struct{ m Model }

// NewIssuesTab is the issue browser as a dashboard tab.
func NewIssuesTab(st Store, opt Options) Tab { return &issuesTab{m: New(st, opt)} }

func (t *issuesTab) Title() string { return "Issues" }
func (t *issuesTab) Init() tea.Cmd { return t.m.Init() }
func (t *issuesTab) View() string  { return t.m.View() }
func (t *issuesTab) Keys() string  { return "j/k move  / search  s sort  f filter  c claim  ? help" }
func (t *issuesTab) Capturing() bool {
	return t.m.mode == modeSearch || t.m.mode == modePrompt || t.m.mode == modePick || t.m.mode == modeCmd
}
func (t *issuesTab) Update(msg tea.Msg) (Tab, tea.Cmd) {
	nm, c := t.m.Update(msg)
	return &issuesTab{m: nm.(Model)}, c
}

// drillMsg asks the shell to open the Issues tab filtered to one milestone.
type drillMsg struct {
	Alias, Milestone string
	None             bool // issues without a milestone
}
