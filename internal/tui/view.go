package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/your-moon/moon/internal/issues"
)

var (
	stOpen   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stClosed = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	stHead   = lipgloss.NewStyle().Bold(true)
	stSel    = lipgloss.NewStyle().Reverse(true)
	stDim    = lipgloss.NewStyle().Faint(true)
	stWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// sanitize makes untrusted text safe to print: no escape sequences, no control characters.
func sanitize(s string) string {
	s = ansi.Strip(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case r == '\n':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func oneLine(s string) string { return strings.ReplaceAll(sanitize(s), "\n", " ") }

// fit truncates or pads s to exactly w display columns.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	if p := w - lipgloss.Width(s); p > 0 {
		s += strings.Repeat(" ", p)
	}
	return s
}

func age(sec int) string {
	switch {
	case sec < 90:
		return strconv.Itoa(sec) + "s"
	case sec < 90*60:
		return strconv.Itoa(sec/60) + "m"
	case sec < 48*3600:
		return strconv.Itoa(sec/3600) + "h"
	}
	return strconv.Itoa(sec/86400) + "d"
}

func stateStyle(state string) lipgloss.Style {
	if state == "open" {
		return stOpen
	}
	return stClosed
}

// View renders the model. It reads the model and nothing else.
func (m Model) View() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	body := m.body()
	lines := strings.Split(body, "\n")
	bodyH := m.h - footerLines
	if bodyH < 1 {
		bodyH = 1
	}
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	lines = lines[:bodyH]
	lines = append(lines, m.footer()...)
	for i, l := range lines {
		if lipgloss.Width(l) > m.w {
			l = ansi.Truncate(l, m.w, "")
		}
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func (m Model) body() string {
	switch {
	case m.mode == modeHelp:
		return strings.Join(wrap(m.helpView(), m.w), "\n")
	case m.mode == modeMenu:
		return m.menuView()
	case m.mode == modePick:
		return m.pickView()
	case m.snap.Missing:
		return "\n  No issue mirror found at " + oneLine(m.opt.MirrorDir) + "\n\n  Create it with:  moon issues pull\n\n  Press q to quit."
	case len(m.all) == 0:
		return "\n  The mirror has no issues yet.\n\n  Fetch them with:  moon issues pull\n\n  Press q to quit."
	case m.full:
		return strings.Join(m.detailPane(m.w, m.h-footerLines), "\n")
	}
	layout, listW, listH, detW, detH, _ := m.geometry()
	switch layout {
	case "list":
		return strings.Join(m.listPane(listW, listH), "\n")
	case "stack":
		out := m.listPane(listW, listH)
		out = append(out, stDim.Render(strings.Repeat("─", m.w)))
		return strings.Join(append(out, m.detailPane(detW, detH)...), "\n")
	}
	l, d := m.listPane(listW, listH), m.detailPane(detW, detH)
	sep := stDim.Render("│")
	out := make([]string, listH)
	for i := range out {
		dl, ll := "", ""
		if i < len(d) {
			dl = d[i]
		}
		if i < len(l) {
			ll = l[i]
		}
		out[i] = fit(ll, listW) + sep + dl
	}
	return strings.Join(out, "\n")
}

// ---------- list ----------

func (m Model) listPane(w, h int) []string {
	refW := 6
	for _, e := range m.rows {
		if n := lipgloss.Width(e.it.Ref); n > refW {
			refW = n
		}
	}
	if refW > 14 {
		refW = 14
	}
	const stW = 6
	msW, lbW := 0, 0
	if w >= 60 {
		msW = 10
	}
	if w >= 72 {
		lbW = 16
	}
	if w >= 100 {
		lbW = 22
	}
	used := 2 + refW + 1 + stW + 1
	if msW > 0 {
		used += msW + 1
	}
	if lbW > 0 {
		used += lbW + 1
	}
	titleW := w - used
	if titleW < 4 {
		titleW = 4
	}
	row := func(mark, ref, st, ms, lb, title string, stStyle *lipgloss.Style) string {
		var b strings.Builder
		b.WriteString(mark + " " + fit(ref, refW) + " ")
		if stStyle != nil {
			b.WriteString(stStyle.Render(fit(st, stW)))
		} else {
			b.WriteString(fit(st, stW))
		}
		b.WriteString(" ")
		if msW > 0 {
			b.WriteString(fit(ms, msW) + " ")
		}
		if lbW > 0 {
			b.WriteString(fit(lb, lbW) + " ")
		}
		b.WriteString(fit(title, titleW))
		return b.String()
	}
	title := "TITLE"
	if m.fMilestone != "" {
		ms := m.fMilestone
		if ms == noMilestone {
			ms = "(none)"
		}
		title += " [milestone: " + oneLine(ms) + "]"
	}
	out := []string{stHead.Render(row(" ", "REF", "STATE", "MILESTONE", "LABELS", title, nil))}
	vis := h - 1
	for i := m.top; i < len(m.rows) && i < m.top+vis; i++ {
		it := &m.rows[i].it
		mark := " "
		if it.Claim != nil && !it.Claim.Stale {
			mark = "@"
		} else if it.Pending > 0 || it.Num == 0 {
			mark = "+"
		}
		ms, lb, ti := oneLine(it.Milestone), oneLine(strings.Join(it.Labels, ",")), oneLine(it.Title)
		if i == m.cursor {
			out = append(out, stSel.Render(row(">", it.Ref, it.State, ms, lb, ti, nil)))
			continue
		}
		st := stateStyle(it.State)
		out = append(out, row(mark, it.Ref, it.State, ms, lb, ti, &st))
	}
	if len(m.rows) == 0 && h > 2 {
		out = append(out, "", "  No issues match. Press f to change the filter.")
	}
	return out
}

// ---------- detail ----------

func wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, l := range strings.Split(sanitize(s), "\n") {
		if strings.TrimSpace(l) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(l, w, ""), "\n")...)
	}
	return out
}

func (m Model) detailLines(w int) []string {
	if w > 1 {
		w-- // one column of padding on the left
	}
	if m.derr != "" {
		return wrap(m.derr, w)
	}
	d := m.det
	if d.Ref == "" {
		return []string{"Nothing selected."}
	}
	st := stateStyle(d.State)
	out := []string{stHead.Render(d.Ref) + "  " + st.Render(oneLine(d.State))}
	for _, l := range wrap(d.Title, w) {
		out = append(out, stHead.Render(l))
	}
	out = append(out, "")
	kv := func(k, v string) { out = append(out, wrap(k+": "+v, w)...) }
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	kv("labels", dash(oneLine(strings.Join(d.Labels, ", "))))
	kv("milestone", dash(oneLine(d.Milestone)))
	if d.Author != "" {
		kv("author", oneLine(d.Author))
	}
	switch c := d.Claim; {
	case c == nil:
		kv("claim", "none")
	case c.Stale:
		kv("claim", oneLine(c.Agent)+" on "+oneLine(c.Host)+", "+age(c.AgeSec)+" ago [stale]")
	default:
		kv("claim", oneLine(c.Agent)+" on "+oneLine(c.Host)+", "+age(c.AgeSec)+" ago")
	}
	for _, p := range d.Pending {
		out = append(out, wrap("queued: "+p, w)...)
	}
	if d.NotKept {
		out = append(out, stWarn.Render("(closed long ago; the body is not kept locally)"))
	}
	out = append(out, stDim.Render(strings.Repeat("─", w)))
	out = append(out, wrap(d.Body, w)...)
	return out
}

func (m Model) detailPane(w, h int) []string {
	lines := m.detailLines(w)
	top := m.dtop
	if max := len(lines) - h; top > max {
		top = max
	}
	if top < 0 {
		top = 0
	}
	out := make([]string, 0, h)
	for i := top; i < len(lines) && len(out) < h; i++ {
		out = append(out, " "+lines[i])
	}
	if top+h < len(lines) && len(out) > 0 {
		out[len(out)-1] = " " + stDim.Render(fmt.Sprintf("… %d more lines (scroll)", len(lines)-top-h+1))
	}
	return out
}

// ---------- help, menu, footer ----------

const helpText = `Keys

  j k, up down     move              g G      top, bottom
  pgup pgdn        page              ctrl-d u scroll the detail
  /                search (same match as ls -q: title or labels)
  s                cycle sort: number, updated, created, title
  r                reverse the sort
  f                filter menu: state, label, milestone, clear
  tab              next repo alias (when several)
  enter  esc       open the detail full screen, go back
  c  u             claim, release (agent: --agent or $USER)
                   refused when another agent holds the claim; never forced
  x                close or reopen (queued locally)
  m                comment (queued locally; $EDITOR, else a one-line prompt)
  M                set the milestone: type to filter, - for none (queued locally)
  1-9  [ ]         switch dashboard tab
  ?                this help        q ctrl-c quit
  mouse            wheel scrolls, click selects

Marks: > selected, @ claimed, + has queued local writes.

Nothing is sent to GitHub from here. This view has no push, pull or sync;
queued writes are sent later with: moon issues sync

Press any key to close.`

func (m Model) helpView() string { return helpText }

func (m Model) menuView() string {
	var b strings.Builder
	line := func(i int, s string) {
		p := "  "
		if i == m.menuCur {
			p = "> "
		}
		b.WriteString(p + s + "\n")
	}
	anyv := func(s string) string {
		if s == "" {
			return "(any)"
		}
		return oneLine(s)
	}
	switch m.menu {
	case menuTop:
		b.WriteString("Filter  (enter selects, esc closes)\n\n")
		line(0, "state:     "+m.fState+"   (enter cycles open, closed, all)")
		line(1, "label:     "+anyv(m.fLabel))
		line(2, "milestone: "+anyv(m.fMilestone))
		line(3, "clear all filters")
	default:
		name := "label"
		if m.menu == menuMilestone {
			name = "milestone"
		}
		b.WriteString("Pick a " + name + "  (enter selects, esc back)\n\n")
		vis := m.h - footerLines - 3
		if vis < 3 {
			vis = 3
		}
		start := 0
		if m.menuCur >= vis {
			start = m.menuCur - vis + 1
		}
		for i := start; i < len(m.menuOpts) && i < start+vis; i++ {
			line(i, anyv(m.menuOpts[i]))
		}
	}
	return b.String()
}

func (m Model) pickView() string {
	var b strings.Builder
	cur := "(none)"
	if it := m.cur(); it != nil {
		if it.Milestone != "" {
			cur = oneLine(it.Milestone)
		}
		b.WriteString("Milestone for " + it.Ref + "  (current: " + cur + ")\n")
	}
	b.WriteString("Type to filter, - for none, enter applies, esc cancels\n\n")
	opts := m.pickMatches()
	vis := m.h - footerLines - 4
	if vis < 3 {
		vis = 3
	}
	start := 0
	if m.pickCur >= vis {
		start = m.pickCur - vis + 1
	}
	for i := start; i < len(opts) && i < start+vis; i++ {
		p, o := "  ", "- (no milestone)"
		if opts[i] != "" {
			o = oneLine(opts[i])
		}
		if i == m.pickCur {
			p = "> "
		}
		b.WriteString(p + o + "\n")
	}
	if len(opts) == 0 {
		b.WriteString("  no milestone matches\n")
	}
	return b.String()
}

func (m Model) footer() []string {
	hints := "j/k move  / search  s sort  f filter  c claim  u release  x close  m comment  ? help  q quit"
	switch m.mode {
	case modeSearch:
		return []string{stDim.Render(fit("enter keep  esc clear  up/down move", m.w)), fit("/"+oneLine(m.input.Value())+"_", m.w)}
	case modePick:
		return []string{stDim.Render(fit("enter apply  esc cancel  up/down move", m.w)), fit("milestone> "+oneLine(m.input.Value())+"_", m.w)}
	case modePrompt:
		return []string{stDim.Render(fit("enter queue the comment  esc cancel", m.w)), fit("comment> "+oneLine(m.input.Value())+"_", m.w)}
	case modeHelp, modeMenu:
		hints = "enter select  esc back"
		if m.mode == modeHelp {
			hints = "any key closes"
		}
	}
	if m.drilled && !m.full && m.mode == modeList {
		hints = "esc clears the milestone filter  " + hints
	}
	if m.full {
		hints = "j/k scroll  esc back  c claim  u release  x close  m comment  ? help  q quit"
	}
	var parts []string
	if m.msg != "" {
		parts = append(parts, oneLine(m.msg))
	}
	dir := "↓"
	if m.reverse != (m.sortBy == sortTitle) {
		dir = "↑"
	}
	info := fmt.Sprintf("%d/%d  state:%s  sort:%s%s", len(m.rows), len(m.all), m.fState, sortNames[m.sortBy], dir)
	ms := m.fMilestone
	if ms == noMilestone {
		ms = "(none)"
	}
	for _, f := range [][2]string{{"repo", m.fAlias}, {"label", m.fLabel}, {"milestone", ms}, {"q", m.query}} {
		if f[1] != "" {
			info += "  " + f[0] + ":" + oneLine(f[1])
		}
	}
	parts = append(parts, info, pulled(m.snap))
	if m.snap.Queued > 0 {
		parts = append(parts, "queued: "+strconv.Itoa(m.snap.Queued))
	}
	return []string{stDim.Render(fit(hints, m.w)), fit(strings.Join(parts, "  |  "), m.w)}
}

func pulled(s issues.Snapshot) string {
	if s.PullAgeSec < 0 {
		return "mirror never pulled"
	}
	return fmt.Sprintf("mirror pulled %d min ago", s.PullAgeSec/60)
}

func ansiStrip(s string) string { return ansi.Strip(s) }
