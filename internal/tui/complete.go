package tui

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/your-moon/moon/internal/completion"
	"github.com/your-moon/moon/internal/issues"
)

// Tab completion in the dashboard. Candidates come from internal/completion, the engine behind
// `moon __complete`; this file only holds the key handling, the candidate list and the ":" command line.

// modelSource gives the engine this model's data: the snapshot already in memory, and the mirror's milestones.
type modelSource struct{ m *Model }

func (s modelSource) Aliases() []string                 { return s.m.snap.Aliases }
func (s modelSource) Items() []issues.Item              { return s.m.snap.Items }
func (s modelSource) Milestones() []issues.MilestoneRow { return s.m.st.Milestones() }
func (s modelSource) PRs() []issues.PRItem              { return nil }

// Scope is the repo the list is on (the project's repo unless a or tab changed it), so tab completion of refs,
// labels and milestones offers that repo's; empty means every repo.
func (s modelSource) Scope() []string {
	if s.m.fAlias != "" {
		return []string{s.m.fAlias}
	}
	return nil
}

func (m *Model) src() completion.Source { return modelSource{m} }

// compState is an open candidate list. start and end are the byte span of the input that Tab replaces.
type compState struct {
	open       bool
	cands      []completion.Candidate
	idx        int // -1 until the first cycling Tab
	start, end int
}

type completer func(text string, cursor int) (start, end int, c []completion.Candidate)

func byteOffset(s string, runes int) int {
	if runes <= 0 {
		return 0
	}
	n := 0
	for i := range s {
		if n == runes {
			return i
		}
		n++
	}
	return len(s)
}

func (m *Model) replaceSpan(start, end int, rep string) {
	v := m.input.Value()
	m.input.SetValue(v[:start] + rep + v[end:])
	m.input.SetCursor(utf8.RuneCountInString(v[:start] + rep))
	m.comp.start, m.comp.end = start, start+len(rep)
}

// tabComplete handles Tab and Shift-Tab: one match completes fully, several complete to their longest
// common prefix and open the list, and further presses cycle through it.
func (m Model) tabComplete(back bool, fn completer) Model {
	if m.comp.open && len(m.comp.cands) > 0 {
		n := len(m.comp.cands)
		switch {
		case back && m.comp.idx <= 0:
			m.comp.idx = n - 1
		case back:
			m.comp.idx--
		default:
			m.comp.idx = (m.comp.idx + 1) % n
		}
		m.replaceSpan(m.comp.start, m.comp.end, m.comp.cands[m.comp.idx].Value)
		return m
	}
	v := m.input.Value()
	cur := byteOffset(v, m.input.Position())
	start, end, cands := fn(v, cur)
	switch len(cands) {
	case 0:
		m.comp = compState{}
	case 1:
		m.replaceSpan(start, end, cands[0].Value)
		m.comp = compState{start: m.comp.start, end: m.comp.end}
	default:
		vals := make([]string, len(cands))
		for i, c := range cands {
			vals[i] = c.Value
		}
		rep := v[start:end]
		if p := completion.CommonPrefix(vals); len(p) > len(v[start:cur]) {
			rep = p
		}
		m.comp = compState{cands: cands, idx: -1}
		m.replaceSpan(start, end, rep)
		m.comp.open = true
	}
	return m
}

func (m Model) searchCompleter() completer {
	return func(t string, c int) (int, int, []completion.Candidate) {
		return completion.CompleteSearch(m.src(), t, c)
	}
}

func (m Model) lineCompleter() completer {
	return func(t string, c int) (int, int, []completion.Candidate) {
		return completion.CompleteLine(m.src(), t, c)
	}
}

const compRows = 6

// compView is the candidate list: a window of compRows rows with the cycled one marked.
func (m Model) compView(w int) []string {
	if !m.comp.open || len(m.comp.cands) == 0 {
		return nil
	}
	n := len(m.comp.cands)
	top := 0
	if m.comp.idx >= compRows {
		top = m.comp.idx - compRows + 1
	}
	vis := min(compRows, n-top)
	width := 0
	for _, c := range m.comp.cands[top : top+vis] {
		width = max(width, utf8.RuneCountInString(oneLine(c.Value)))
	}
	width = min(width, max(10, w/2))
	var out []string
	for i := top; i < top+vis; i++ {
		c := m.comp.cands[i]
		mark := "  "
		if i == m.comp.idx {
			mark = "> "
		}
		v := fit(oneLine(c.Value), width)
		line := mark + v + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(v)))
		if c.Desc != "" {
			line += "  " + stDim.Render(oneLine(c.Desc))
		}
		out = append(out, fit(line, w))
	}
	if n > top+vis {
		out[len(out)-1] = fit(out[len(out)-1], max(1, w-12)) + stDim.Render("  … "+itoa(n-top-vis)+" more")
	}
	return out
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}

// ---------- the ":" command line ----------

func (m *Model) startCmd() {
	m.mode = modeCmd
	m.full = false
	m.comp = compState{}
	m.histPos = len(m.hist)
	m.input.Prompt = ""
	m.input.SetValue("")
	m.input.Focus()
}

func (m Model) keyCmd(k tea.KeyMsg) (Model, tea.Cmd) {
	switch s := k.String(); s {
	case "tab", "shift+tab":
		return m.tabComplete(s == "shift+tab", m.lineCompleter()), nil
	case "esc":
		if m.comp.open {
			m.comp = compState{}
			return m, nil
		}
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		line := strings.TrimSpace(m.input.Value())
		m.mode = modeList
		m.comp = compState{}
		m.input.Blur()
		m.input.SetValue("")
		if line == "" {
			return m, nil
		}
		if n := len(m.hist); n == 0 || m.hist[n-1] != line {
			m.hist = append(m.hist, line)
		}
		return m.runLine(line)
	case "up", "ctrl+p":
		m.comp = compState{}
		if m.histPos > 0 {
			m.histPos--
			m.input.SetValue(m.hist[m.histPos])
			m.input.CursorEnd()
		}
		return m, nil
	case "down", "ctrl+n":
		m.comp = compState{}
		if m.histPos < len(m.hist) {
			m.histPos++
		}
		if m.histPos < len(m.hist) {
			m.input.SetValue(m.hist[m.histPos])
		} else {
			m.input.SetValue("")
		}
		m.input.CursorEnd()
		return m, nil
	}
	m.comp = compState{}
	m.input, _ = m.input.Update(k)
	return m, nil
}

// itemByRef finds an issue by its ref among the loaded ones.
func (m *Model) itemByRef(ref string) *issues.Item {
	for i := range m.snap.Items {
		if m.snap.Items[i].Ref == ref {
			return &m.snap.Items[i]
		}
	}
	return nil
}

// runLine executes one ":" command. Writes go through the Store, the same path as the CLI (outbox and
// claims); every failure becomes a footer message.
func (m Model) runLine(line string) (Model, tea.Cmd) {
	ws, _ := completion.SplitWords(line)
	args := make([]string, len(ws))
	for i, w := range ws {
		args[i] = w.Text
	}
	cmd, rest := args[0], args[1:]
	needRef := func() (*issues.Item, bool) {
		if len(rest) == 0 {
			m.msg = ":" + cmd + " needs an issue, for example :" + cmd + " " + firstRef(m)
			return nil, false
		}
		it := m.itemByRef(rest[0])
		if it == nil {
			m.msg = "unknown ref " + rest[0]
			return nil, false
		}
		return it, true
	}
	switch cmd {
	case "q", "quit":
		return m, tea.Quit
	case "claim", "release", "close", "reopen":
		it, ok := needRef()
		if !ok {
			return m, nil
		}
		m.act(cmd, it.Ref)
	case "comment":
		it, ok := needRef()
		if !ok {
			return m, nil
		}
		m.queueComment(it.Ref, strings.Join(rest[1:], " "))
	case "label":
		it, ok := needRef()
		if !ok {
			return m, nil
		}
		if len(rest) < 3 || (rest[1] != "add" && rest[1] != "remove") {
			m.msg = "usage: :label <ref> add|remove <label>"
			return m, nil
		}
		m.act("label", it.Ref, rest[1:]...)
	case "milestone":
		it, ok := needRef()
		if !ok {
			return m, nil
		}
		if len(rest) < 2 {
			m.msg = "usage: :milestone <ref> <title|none>"
			return m, nil
		}
		m.act("milestone", it.Ref, rest[1:]...)
	case "sort":
		m.lineSort(rest)
	case "filter":
		m.lineFilter(rest)
	case "repo":
		switch {
		case len(rest) == 0 || rest[0] == "all":
			m.fAlias = ""
		case contains(m.snap.Aliases, rest[0]):
			m.fAlias = rest[0]
		default:
			m.msg = "unknown repo " + rest[0]
			return m, nil
		}
		m.refilter()
	default:
		m.msg = "unknown command :" + cmd + " (Tab lists them)"
	}
	return m, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func firstRef(m Model) string {
	if it := m.cur(); it != nil {
		return it.Ref
	}
	return "app#1"
}

func (m *Model) lineSort(args []string) {
	for _, a := range args {
		if a == "reverse" || a == "-r" {
			m.reverse = !m.reverse
			continue
		}
		found := false
		for i, n := range sortNames {
			if strings.HasPrefix(n, a) && a != "" {
				m.sortBy, found = sortKey(i), true
				break
			}
		}
		if !found {
			m.msg = "unknown sort " + a + " (number, updated, created, title, reverse)"
			return
		}
	}
	m.refilter()
}

func (m *Model) lineFilter(args []string) {
	for _, a := range args {
		if a == "clear" {
			m.fState, m.fLabel, m.fMilestone, m.fAlias = "open", "", "", ""
			continue
		}
		k, v, ok := strings.Cut(a, ":")
		if !ok {
			m.msg = "filter wants state:X label:X milestone:X repo:X or clear"
			return
		}
		switch k {
		case "state":
			if !contains(stateCycle, v) {
				m.msg = "state must be open, closed or all"
				return
			}
			m.fState = v
		case "label":
			m.fLabel = v
		case "milestone":
			m.fMilestone = v
		case "repo":
			if v != "" && !contains(m.snap.Aliases, v) {
				m.msg = "unknown repo " + v
				return
			}
			m.fAlias = v
		default:
			m.msg = "unknown filter " + k
			return
		}
	}
	m.refilter()
}
