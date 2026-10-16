package issues

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ms is one row of <alias>/milestones.tsv: title, state, closed, open, due. raw is the file line.
type ms struct {
	repo, title, state, due, raw string
	closed, open                 int
}

func (m ms) total() int { return m.closed + m.open }

// pct is the share of closed issues, rounded DOWN so that 99.6% reads 99 and only a truly finished
// milestone shows 100. A milestone with no issues is 0.
func (m ms) pct() int {
	if m.total() == 0 {
		return 0
	}
	return m.closed * 100 / m.total()
}

func (a *App) readMilestones(shorts []string) []ms {
	var out []ms
	for _, short := range shorts {
		var lines []string
		if a.localOnly(short) {
			lines = a.localMilestones(short)
		} else if s, err := readText(filepath.Join(a.Home, short, "milestones.tsv")); err == nil {
			lines = splitLines(s)
		}
		for _, line := range lines {
			f := strings.Split(line, "\t")
			for len(f) < 5 {
				f = append(f, "")
			}
			c, _ := strconv.Atoi(f[2])
			o, _ := strconv.Atoi(f[3])
			out = append(out, ms{repo: short, title: f[0], state: f[1], due: f[4], raw: short + "\t" + line, closed: c, open: o})
		}
	}
	return out
}

// bar draws progress in w cells. Filled cells are rounded down, so a bar is full only when everything is closed.
func bar(closed, total, w int, unicode bool) string {
	filled := 0
	if total > 0 {
		filled = closed * w / total
	}
	if unicode {
		return strings.Repeat("█", filled) + strings.Repeat("░", w-filled)
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", w-filled) + "]"
}

func (a *App) barWidth() int { return max(6, min(24, a.Width/6)) }

func (a *App) utf8Locale() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := a.Environ(k); v != "" {
			u := strings.ToUpper(v)
			return strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8")
		}
	}
	return false
}

// barRender draws the filled part in the percent role and the empty part muted.
func (a *App) barRender(m ms) func(*Styler) string {
	return func(st *Styler) string { return a.barParts(m, st) }
}

func (a *App) barText(m ms) string { return a.barParts(m, a.styler()) }

func (a *App) barParts(m ms, st *Styler) string {
	b := a.bar(m)
	if st == nil || !st.On {
		return b
	}
	n := 0
	for _, r := range b {
		if r == '█' || r == '#' {
			n++
		}
	}
	r := []rune(b)
	// find the end of the filled run (after an optional leading "[")
	lead := 0
	if len(r) > 0 && r[0] == '[' {
		lead = 1
	}
	fill, rest := string(r[:lead+n]), string(r[lead+n:])
	return st.P(BarRole(m.pct()), fill) + st.P("bar-empty", rest)
}

func (a *App) bar(m ms) string {
	return bar(m.closed, m.total(), a.barWidth(), a.Color && a.utf8Locale())
}

func (a *App) progressColor(m ms) string { return BarRole(m.pct()) }

func (a *App) cmdMilestones(o *Opts) error {
	v := a.resolveView(o, true)
	all := a.readMilestones(a.shortsFor(o))
	state := o.str("state")
	if state == "" && v.format == "table" && a.TTY {
		state = "open"
	}
	var rows []ms
	for _, m := range all {
		if state == "" || state == "all" || m.state == state {
			rows = append(rows, m)
		}
	}
	if key := o.str("sort"); key != "" || o.B["reverse"] {
		less := func(x, y ms) bool { return false }
		switch key {
		case "due": // soonest first; no due date last
			less = func(x, y ms) bool {
				if (x.due == "") != (y.due == "") {
					return x.due != ""
				}
				return x.due < y.due
			}
		case "progress":
			less = func(x, y ms) bool { return x.pct() > y.pct() }
		case "open":
			less = func(x, y ms) bool { return x.open > y.open }
		case "title":
			less = func(x, y ms) bool { return strings.ToLower(x.title) < strings.ToLower(y.title) }
		}
		rows = stableSort(rows, less, o.B["reverse"], key == "")
	}
	total := len(rows)
	rows = limitRows(rows, v.limit)
	switch v.format {
	case "json":
		l := []any{}
		for _, m := range rows {
			l = append(l, Obj{{"repo", m.repo}, {"title", m.title}, {"state", m.state}, {"closed", m.closed}, {"open", m.open},
				{"total", m.total()}, {"percent", m.pct()}, {"due_on", nilIfEmpty(m.due)}})
		}
		a.println(dumps(l, false))
	case "table":
		if len(rows) == 0 {
			if len(all) == 0 {
				fmt.Fprintln(a.Err, "no milestones in the mirror (run moon issues pull, or none are defined)")
			} else {
				fmt.Fprintln(a.Err, "no milestones match (try --state all)")
			}
			return nil
		}
		fmt.Fprint(a.Out, a.milestoneTable(rows))
		a.footer(len(rows), total)
	default:
		for _, m := range rows {
			a.println(m.raw)
		}
	}
	return nil
}

func (a *App) milestoneTable(rows []ms) string {
	multi := false
	for _, m := range rows {
		if m.repo != rows[0].repo {
			multi = true
		}
	}
	today := a.Now().UTC().Format("2006-01-02")
	cols := []col{}
	if multi {
		cols = append(cols, col{head: "REPO"})
	}
	cols = append(cols, col{head: "TITLE", flex: true}, col{head: "PROGRESS"}, col{head: "CLOSED/TOTAL"}, col{head: "PCT"}, col{head: "OPEN"}, col{head: "DUE"})
	cells := make([][]cell, len(rows))
	for i, m := range rows {
		due, dc := "-", cDim
		if len(m.due) >= 10 {
			due, dc = m.due[:10], ""
			if m.state == "open" && due < today {
				dc = "error"
			}
		}
		c := []cell{}
		if multi {
			c = append(c, plain(m.repo))
		}
		pc := a.progressColor(m)
		c = append(c, plain(m.title), cell{s: a.bar(m), color: pc, render: a.barRender(m)}, plain(fmt.Sprintf("%d/%d", m.closed, m.total())), cell{s: fmt.Sprintf("%d%%", m.pct()), color: pc},
			plain(strconv.Itoa(m.open)), cell{s: due, color: dc})
		cells[i] = c
	}
	return renderTable(cols, cells, a.Width, a.styler())
}

// stableSort orders by less (ties keep their order, also when reversed); with plainReverse it just flips the list.
func stableSort[T any](items []T, less func(x, y T) bool, reverse, plainReverse bool) []T {
	out := append([]T{}, items...)
	if plainReverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if reverse {
			return less(out[j], out[i])
		}
		return less(out[i], out[j])
	})
	return out
}

// matchMilestone resolves what the user typed against known titles: exact, then case-insensitive, then a
// unique prefix (case-insensitive). ambiguous lists the candidates when a prefix matches several.
func matchMilestone(typed string, known []string) (title string, ambiguous []string, ok bool) {
	for _, k := range known {
		if k == typed {
			return k, nil, true
		}
	}
	lt := strings.ToLower(typed)
	var ci, pre []string
	seen := map[string]bool{}
	for _, k := range known {
		if seen[k] {
			continue
		}
		seen[k] = true
		lk := strings.ToLower(k)
		if lk == lt {
			ci = append(ci, k)
		} else if strings.HasPrefix(lk, lt) {
			pre = append(pre, k)
		}
	}
	if len(ci) == 1 {
		return ci[0], nil, true
	}
	if len(ci) > 1 {
		return "", ci, false
	}
	if len(pre) == 1 {
		return pre[0], nil, true
	}
	return "", pre, false
}

// resolveMilestone rewrites --milestone to the real title. Unknown titles exit 2 with the close matches.
// With no milestones known at all (nothing pulled) the value is left alone, as before.
func (a *App) resolveMilestone(o *Opts) error {
	typed := o.str("milestone")
	if typed == "" {
		return nil
	}
	shorts := a.shortsFor(o)
	var known []string
	for _, m := range a.readMilestones(shorts) {
		known = append(known, m.title)
	}
	for _, short := range shorts {
		for _, r := range a.readIndex(short) {
			if r[2] != "" {
				known = append(known, r[2])
			}
		}
	}
	if len(known) == 0 {
		return nil
	}
	t, amb, ok := matchMilestone(typed, known)
	if ok {
		o.S["milestone"] = t
		return nil
	}
	if len(amb) > 0 {
		sort.Strings(amb)
		return die("milestone '%s' is ambiguous: %s", typed, strings.Join(amb, ", "))
	}
	uniq := map[string]bool{}
	var titles []string
	for _, k := range known {
		if !uniq[k] {
			uniq[k] = true
			titles = append(titles, k)
		}
	}
	sort.Strings(titles)
	var close []string
	for _, k := range titles {
		if closest(typed, []string{k}) != "" {
			close = append(close, k)
		}
	}
	if len(close) > 0 {
		return die("no milestone '%s'; did you mean %s?", typed, strings.Join(close, " or "))
	}
	return die("no milestone '%s'; known milestones: %s", typed, strings.Join(titles, ", "))
}

func (a *App) cmdMilestoneShow(o *Opts) error {
	o.S["milestone"] = o.Pos[0]
	if err := a.resolveFilters(o); err != nil {
		return err
	}
	rows := a.collect(o, o.str("state"), "")
	v := a.resolveView(o, true)
	if v.format == "table" {
		var hit []ms
		for _, m := range a.readMilestones(a.shortsFor(o)) {
			if m.title == o.str("milestone") {
				hit = append(hit, m)
			}
		}
		for _, m := range hit {
			a.println(a.P(cBold, m.title) + "  " + a.P(stateColor(m.state), m.state) + "  (" + m.repo + ")")
			due := "no due date"
			if len(m.due) >= 10 {
				due = "due " + m.due[:10]
			}
			a.printf("%s  %d/%d closed · %d%% · %d open · %s\n\n", a.barText(m), m.closed, m.total(), m.pct(), m.open, due)
		}
	}
	return a.listRows(o, rows)
}
