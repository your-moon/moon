package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/your-moon/moon/internal/issues"
)

// Styles are built from the issues palette (the same roles the CLI tables use). SetPalette rebuilds them.
var (
	stOpen   lipgloss.Style
	stClosed lipgloss.Style
	stHead   = lipgloss.NewStyle().Bold(true)
	stSel    = lipgloss.NewStyle().Reverse(true)
	stDim    = lipgloss.NewStyle().Faint(true)
	stWarn   lipgloss.Style
	pal      issues.Palette
)

func init() { SetPalette(issues.NewPalette("dark", nil)) }

// lipColor is a palette colour as a Lip Gloss colour: an ANSI index for a named colour, else its hex (Lip Gloss
// degrades it to the terminal's depth).
func lipColor(c issues.Color) lipgloss.Color {
	if c.Named >= 0 {
		return lipgloss.Color(strconv.Itoa(c.Named))
	}
	return lipgloss.Color(c.RGB.Hex())
}

// SetPalette sets the colours of the terminal UI from the palette.
func SetPalette(p issues.Palette) {
	pal = p
	stOpen = lipgloss.NewStyle().Foreground(lipColor(p["state-open"]))
	stClosed = lipgloss.NewStyle().Foreground(lipColor(p["state-closed"]))
	stWarn = lipgloss.NewStyle().Foreground(lipColor(p["warning"]))
}

func roleStyle(role string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipColor(pal[role]))
}

func statusRole(name string) string {
	switch name {
	case issues.StWorking, issues.StReview, issues.StStale, issues.StBlocked, issues.StReady, issues.StDone:
		return "status-" + strings.ReplaceAll(strings.ReplaceAll(name, "in review", "review"), " ", "-")
	}
	return ""
}

func statusStyle(name string) lipgloss.Style {
	if r := statusRole(name); r != "" {
		return roleStyle(r)
	}
	return lipgloss.NewStyle()
}

// chip draws a label on its GitHub colour (or its prefix's palette colour) with black or white text.
func chip(shown, stored string, colors map[string]string) string {
	fg, bg := issues.ChipColors(pal.LabelColor(stored, colors[stored]))
	return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).Render(shown)
}

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

// statusText is the status as the list shows it: its name, with "!" when a closed issue still has a claim.
func statusText(w issues.Status) string {
	if w.Name == issues.StDone && w.StaleClaim {
		return "done!"
	}
	return w.Name
}

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
	statW := lipgloss.Width("STATUS")
	for _, e := range m.rows {
		statW = max(statW, lipgloss.Width(statusText(e.it.Work)))
	}
	msW := 0
	if w >= 100 {
		msW = 10
	}
	mode := m.effLabelMode()
	used := 2 + refW + 1 + stW + 1 + statW + 1
	if msW > 0 {
		used += msW + 1
	}
	lbW := 0
	if mode != issues.LabelsNone {
		texts := make([]string, len(m.rows))
		for i, e := range m.rows {
			texts[i] = issues.LabelsText(e.it.Labels, mode)
		}
		capW := issues.LabelColWidth(texts, "LABELS", w)
		capW = min(capW, max(6, w-used-10)) // the title keeps at least 10 columns when it can
		lbW = lipgloss.Width("LABELS")      // as wide as the widest text that is kept, never past the cap
		for _, e := range m.rows {
			lbW = max(lbW, lipgloss.Width(issues.FitLabels(oneLineAll(e.it.Labels), mode, capW)))
		}
		used += lbW + 1
	}
	titleW := w - used
	if titleW < 4 {
		titleW = 4
	}
	// row lays out one line. lb is the labels cell, already padded to lbW (it may carry colour); stStyle and
	// wkStyle colour the state and status cells (nil: plain, for the header and the selected row).
	row := func(mark, ref, st, wk, ms, lb, title string, stStyle, wkStyle *lipgloss.Style) string {
		var b strings.Builder
		b.WriteString(mark + " " + fit(ref, refW) + " ")
		if stStyle != nil {
			b.WriteString(stStyle.Render(fit(st, stW)))
		} else {
			b.WriteString(fit(st, stW))
		}
		b.WriteString(" ")
		if wkStyle != nil {
			b.WriteString(wkStyle.Render(fit(wk, statW)))
		} else {
			b.WriteString(fit(wk, statW))
		}
		b.WriteString(" ")
		if msW > 0 {
			b.WriteString(fit(ms, msW) + " ")
		}
		if lbW > 0 {
			b.WriteString(lb + " ")
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
	out := []string{stHead.Render(row(" ", "REF", "STATE", "STATUS", "MILESTONE", fit("LABELS", lbW), title, nil, nil))}
	vis := h - 1
	for i := m.top; i < len(m.rows) && i < m.top+vis; i++ {
		it := &m.rows[i].it
		mark := " "
		if it.Claim != nil && !it.Claim.Stale {
			mark = "@"
		} else if it.Pending > 0 || it.Num == 0 {
			mark = "+"
		}
		ms, ti := oneLine(it.Milestone), oneLine(it.Title)
		plainLb := ""
		if lbW > 0 {
			plainLb = issues.FitLabels(oneLineAll(it.Labels), mode, lbW)
		}
		if i == m.cursor {
			out = append(out, stSel.Render(row(">", it.Ref, it.State, statusText(it.Work), ms, fit(plainLb, lbW), ti, nil, nil)))
			continue
		}
		lb := fit(plainLb, lbW)
		if lbW > 0 && len(it.Labels) > 0 {
			shown, stored, more := issues.FitLabelParts(oneLineAll(it.Labels), mode, lbW)
			var parts []string
			for k, n := range shown {
				parts = append(parts, chip(n, stored[k], m.snap.LabelColors[it.Alias]))
			}
			rich := strings.Join(parts, ",")
			if more > 0 {
				if len(shown) > 0 {
					rich += " "
				}
				rich += stDim.Render("+" + strconv.Itoa(more))
			}
			lb = rich + strings.Repeat(" ", max(0, lbW-lipgloss.Width(plainLb)))
		}
		st, wk := stateStyle(it.State), statusStyle(it.Work.Name)
		if it.Work.Name == issues.StDone || it.Work.Name == issues.StStale {
			wk = wk.Faint(it.Work.Name == issues.StDone)
		}
		out = append(out, row(mark, it.Ref, it.State, statusText(it.Work), ms, lb, ti, &st, &wk))
	}
	if len(m.rows) == 0 && h > 2 {
		out = append(out, "", "  No issues match. Press f to change the filter.")
	}
	return out
}

func oneLineAll(l []string) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = oneLine(s)
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
	out = append(out, m.labelLines(d, w)...)
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
	out = append(out, "status: "+statusStyle(d.Work.Name).Render(oneLine(d.Work.Label())))
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

// labelLines lists every label of the issue as chips, in importance order, wrapped onto more lines when they
// do not fit. A label is never cut (one longer than the pane is the only exception, and wraps by the column).
func (m Model) labelLines(d issues.Detail, w int) []string {
	if len(d.Labels) == 0 {
		return []string{"labels: -"}
	}
	alias, _, _ := strings.Cut(d.Ref, "#")
	colors := m.snap.LabelColors[alias]
	ordered := issues.OrderLabels(oneLineAll(d.Labels))
	const lead = "labels:"
	var lines []string
	cur, curW, n := lead, len(lead), 0
	for i, l := range ordered {
		tail := ""
		if i < len(ordered)-1 {
			tail = ","
		}
		if n > 0 && curW+1+lipgloss.Width(l)+len(tail) > w {
			lines = append(lines, cur)
			cur, curW, n = strings.Repeat(" ", len(lead)), len(lead), 0
		}
		cur += " " + chip(l, l, colors) + tail
		curW += 1 + lipgloss.Width(l) + len(tail)
		n++
	}
	return append(lines, cur)
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
  s                cycle sort: number, updated, created, title, status
  r                reverse the sort
  w                only issues being worked on now (status working); again for all
  l                labels column: full, short (kind:/area:/triage: dropped), hidden
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
STATUS: working (a live claim), in review (an open PR closes it), stale (claim past its ttl or its
process is gone), blocked, ready, open, done. done! = closed but a claim is still on file.

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
	hints := "j/k move  / search  s sort  f filter  w working  l labels  c claim  u release  x close  m comment  ? help  q quit"
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
	if m.fWorking {
		info += "  status:working"
	}
	lm := m.effLabelMode()
	if lm == issues.LabelsNone {
		lm = "hidden"
	}
	info += "  labels:" + lm
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
		parts = append(parts, "queued: "+strconv.Itoa(m.snap.Queued)+" "+plural(m.snap.Queued, "op", "ops"))
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
