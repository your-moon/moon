package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/your-moon/moon/internal/issues"
)

// MilestoneStore is what the Milestones tab reads. *issues.Session implements it.
type MilestoneStore interface {
	Milestones() []issues.MilestoneRow
	Load() issues.Snapshot
}

// percent is the share of closed issues, floored: 996 of 1000 is 99, never a rounded-up 100.
// Only a fully closed milestone shows 100. A milestone with no issues shows 0.
func percent(closed, total int) int {
	if total <= 0 || closed <= 0 {
		return 0
	}
	if closed >= total {
		return 100
	}
	return closed * 100 / total
}

// bar draws closed/total in w columns. Plain ASCII [####----] when ascii, else block characters.
// It is full only when everything is closed.
func bar(closed, total, w int, ascii bool) string {
	inner := w
	if ascii {
		inner = w - 2
	}
	if inner < 1 {
		inner = 1
	}
	fill := 0
	if total > 0 && closed > 0 {
		fill = closed * inner / total
		if closed < total && fill >= inner {
			fill = inner - 1
		}
	}
	if ascii {
		return "[" + strings.Repeat("#", fill) + strings.Repeat("-", inner-fill) + "]"
	}
	return roleStyle(issues.BarRole(percent(closed, total))).Render(strings.Repeat("█", fill)) + roleStyle("bar-empty").Render(strings.Repeat("░", inner-fill))
}

type msRow struct {
	alias, title string
	closed, open int
	due          string
	none         bool // the (no milestone) row
}

func (r msRow) total() int { return r.closed + r.open }

type msSort int

const (
	msDue msSort = iota
	msProgress
	msTitle
	msOpen
)

var msSortNames = [...]string{"due", "progress", "title", "open"}

// milestonesTab lists milestones with progress, from the mirror only.
type milestonesTab struct {
	st       MilestoneStore
	rows     []msRow // all
	view     []msRow // shown
	cur, top int
	w, h     int
	sortBy   msSort
	hideDone bool
	multi    bool
	ascii    bool
}

// NewMilestonesTab builds the tab. The bar is plain ASCII under NO_COLOR, a dumb terminal or a pipe.
func NewMilestonesTab(st MilestoneStore) Tab {
	t := &milestonesTab{st: st, ascii: lipgloss.ColorProfile() == termenv.Ascii}
	t.load()
	return t
}

func (t *milestonesTab) Title() string   { return "Milestones" }
func (t *milestonesTab) Init() tea.Cmd   { return nil }
func (t *milestonesTab) Capturing() bool { return false }
func (t *milestonesTab) Keys() string {
	return "j/k move  enter issues  s sort  x hide done  r reload  q quit"
}

func (t *milestonesTab) load() {
	t.rows = t.rows[:0:0]
	have := map[string]bool{}
	for _, r := range t.st.Milestones() {
		t.rows = append(t.rows, msRow{alias: r.Alias, title: r.Title, closed: r.Closed, open: r.Open, due: r.Due})
		have[r.Alias] = true
	}
	snap := t.st.Load()
	t.multi = len(snap.Aliases) > 1
	cnt := map[string]*msRow{}
	for _, it := range snap.Items {
		if it.Milestone != "" {
			continue
		}
		r := cnt[it.Alias]
		if r == nil {
			r = &msRow{alias: it.Alias, title: "(no milestone)", none: true}
			cnt[it.Alias] = r
		}
		if it.State == "open" {
			r.open++
		} else {
			r.closed++
		}
	}
	for _, a := range snap.Aliases {
		if r := cnt[a]; r != nil {
			t.rows = append(t.rows, *r)
		}
	}
	t.resort()
}

func (t *milestonesTab) resort() {
	t.view = t.view[:0:0]
	for _, r := range t.rows {
		if t.hideDone && !r.none && r.open == 0 && r.closed > 0 {
			continue
		}
		t.view = append(t.view, r)
	}
	less := func(a, b msRow) bool {
		if a.none != b.none { // (no milestone) always last
			return b.none
		}
		switch t.sortBy {
		case msDue:
			if (a.due == "") != (b.due == "") {
				return b.due == ""
			}
			if a.due != b.due {
				return a.due < b.due
			}
		case msProgress:
			if pa, pb := percent(a.closed, a.total()), percent(b.closed, b.total()); pa != pb {
				return pa > pb
			}
		case msOpen:
			if a.open != b.open {
				return a.open > b.open
			}
		}
		if a.title != b.title {
			return a.title < b.title
		}
		return a.alias < b.alias
	}
	sort.SliceStable(t.view, func(i, j int) bool { return less(t.view[i], t.view[j]) })
}

func (t *milestonesTab) vis() int {
	if v := t.h - footerLines - 1; v > 1 {
		return v
	}
	return 1
}

func (t *milestonesTab) Update(msg tea.Msg) (Tab, tea.Cmd) {
	n := *t
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		n.w, n.h = msg.Width, msg.Height
	case tea.MouseMsg:
		switch {
		case msg.Action != tea.MouseActionPress:
		case msg.Button == tea.MouseButtonWheelDown:
			n.cur += 3
		case msg.Button == tea.MouseButtonWheelUp:
			n.cur -= 3
		case msg.Button == tea.MouseButtonLeft && msg.Y >= 1 && n.top+msg.Y-1 < len(n.view):
			n.cur = n.top + msg.Y - 1
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			n.cur++
		case "k", "up":
			n.cur--
		case "g", "home":
			n.cur = 0
		case "G", "end":
			n.cur = len(n.view) - 1
		case "s":
			n.sortBy = (n.sortBy + 1) % msSort(len(msSortNames))
			n.resort()
		case "x":
			n.hideDone = !n.hideDone
			n.resort()
		case "r":
			n.load()
		case "enter":
			if n.cur >= 0 && n.cur < len(n.view) {
				r := n.view[n.cur]
				cmd = func() tea.Msg { return drillMsg{Alias: r.alias, Milestone: r.title, None: r.none} }
			}
		case "q", "ctrl+c":
			return &n, tea.Quit
		}
	}
	if n.cur >= len(n.view) {
		n.cur = len(n.view) - 1
	}
	if n.cur < 0 {
		n.cur = 0
	}
	if n.cur < n.top {
		n.top = n.cur
	}
	if n.cur >= n.top+n.vis() {
		n.top = n.cur - n.vis() + 1
	}
	return &n, cmd
}

func (t *milestonesTab) View() string {
	if t.w <= 0 || t.h <= 0 {
		return ""
	}
	titleW := 8
	for _, r := range t.view {
		titleW = max(titleW, lipgloss.Width(oneLine(r.title)))
	}
	titleW = min(titleW, 24)
	aliasW := 0
	if t.multi {
		aliasW = 6
	}
	const countW = 7 // "12/340"
	showDue := t.w >= 70
	// fixed: mark(2) + title + alias + count + pct(5) + open(5) + closed(7) + due(11) + gaps
	fixed := 2 + titleW + 1 + countW + 1 + 5 + 1 + 5 + 1 + 6 + 1
	if aliasW > 0 {
		fixed += aliasW + 1
	}
	if showDue {
		fixed += 11
	}
	barW := t.w - fixed
	if barW < 6 {
		barW = 6
		titleW = max(4, titleW-(6-(t.w-fixed)))
	}
	barW = min(barW, 30)
	line := func(mark, alias, title, bars, count, pct, open, closed, due string) string {
		var b strings.Builder
		b.WriteString(mark + " ")
		if aliasW > 0 {
			b.WriteString(fit(alias, aliasW) + " ")
		}
		b.WriteString(fit(title, titleW) + " " + bars + " " + fit(count, countW) + " " + fit(pct, 5) + " " + fit(open, 5) + " " + fit(closed, 6))
		if showDue {
			b.WriteString(" " + fit(due, 10))
		}
		return b.String()
	}
	lines := []string{stHead.Render(fit(line(" ", "REPO", "MILESTONE", fit("PROGRESS", barW), "DONE", "PCT", "OPEN", "CLOSED", "DUE"), t.w))}
	if len(t.view) == 0 {
		msg := "  No milestones in the mirror. Pull them with: moon issues pull"
		if t.hideDone && len(t.rows) > 0 {
			msg = "  Every milestone is fully closed. Press x to show them."
		}
		lines = append(lines, "", msg)
	}
	for i := t.top; i < len(t.view) && i < t.top+t.vis(); i++ {
		r := t.view[i]
		due := "-"
		if len(r.due) >= 10 {
			due = r.due[:10]
		}
		mark := " "
		if i == t.cur {
			mark = ">"
		}
		s := line(mark, r.alias, oneLine(r.title), bar(r.closed, r.total(), barW, t.ascii), fmt.Sprintf("%d/%d", r.closed, r.total()),
			fmt.Sprintf("%d%%", percent(r.closed, r.total())), fmt.Sprint(r.open), fmt.Sprint(r.closed), due)
		if i == t.cur {
			s = stSel.Render(ansiStrip(s))
		}
		lines = append(lines, s)
	}
	body := max(t.h-footerLines, 1)
	for len(lines) < body {
		lines = append(lines, "")
	}
	lines = lines[:body]
	hide := ""
	if t.hideDone {
		hide = "  fully closed hidden"
	}
	lines = append(lines, stDim.Render(fit(t.Keys(), t.w)), fit(fmt.Sprintf("%d shown of %d  sort:%s%s", len(t.view), len(t.rows), msSortNames[t.sortBy], hide), t.w))
	for i, l := range lines {
		if lipgloss.Width(l) > t.w {
			l = ansi.Truncate(l, t.w, "")
		}
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}
