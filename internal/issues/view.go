package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// defaultTTYLimit is how many rows a terminal gets when --limit is not given.
const defaultTTYLimit = 30

type view struct {
	format string // table, tsv or json
	limit  int    // 0 = all
}

// resolveView turns --format/--limit and the terminal state into what to print. "auto" is a table on a
// terminal and the tab-separated text everywhere else, so pipes and agents see what they always saw.
func (a *App) resolveView(o *Opts, long bool) view {
	v := view{format: o.str("format")}
	if o.B["json"] {
		v.format = "json"
	}
	if v.format == "" || v.format == "auto" {
		v.format = "tsv"
		if a.TTY || a.Forced { // --color always is for pagers (less -R): they want the table
			v.format = "table"
		}
	}
	if o.Seen["limit"] {
		v.limit, _ = strconv.Atoi(o.str("limit"))
	} else if long && a.TTY && v.format == "table" {
		v.limit = defaultTTYLimit
	}
	if v.format != "table" {
		a.Color = false
	}
	return v
}

// sortedBy returns a stable order: ties keep their incoming order, in both directions.
// number, created and updated put the newest first (like the default listing); title is A-Z.
func sortedBy[T any](items []T, key string, reverse bool, num func(T) int, created, updated, title func(T) string, rank func(T) int) []T {
	if key == "" && !reverse {
		return items
	}
	if key == "" { // -r alone: the default order, backwards
		out := make([]T, len(items))
		for i, it := range items {
			out[len(items)-1-i] = it
		}
		return out
	}
	less := func(x, y T) bool { return false }
	switch key {
	case "number":
		less = func(x, y T) bool { return num(x) > num(y) }
	case "created":
		less = func(x, y T) bool { return created(x) > created(y) }
	case "updated":
		less = func(x, y T) bool { return updated(x) > updated(y) }
	case "title":
		less = func(x, y T) bool { return strings.ToLower(title(x)) < strings.ToLower(title(y)) }
	case "status": // StatusOrder, most in need of attention first; ties keep their order
		if rank != nil {
			less = func(x, y T) bool { return rank(x) < rank(y) }
		}
	}
	out := append([]T{}, items...)
	sort.SliceStable(out, func(i, j int) bool {
		if reverse {
			return less(out[j], out[i])
		}
		return less(out[i], out[j])
	})
	return out
}

func limitRows[T any](items []T, n int) []T {
	if n > 0 && len(items) > n {
		return items[:n]
	}
	return items
}

func (a *App) footer(shown, total int) {
	if shown < total {
		fmt.Fprintf(a.Err, "showing %d of %d (use --limit 0 for all)\n", shown, total)
	} else {
		fmt.Fprintf(a.Err, "showing %d of %d\n", shown, total)
	}
}

// noMirror lists the aliases that have never been pulled.
func (a *App) noMirror(shorts []string) []string {
	var out []string
	for _, s := range shorts {
		if a.localOnly(s) {
			continue
		}
		if _, err := os.Stat(filepath.Join(a.Home, s, "index.tsv")); err != nil {
			out = append(out, s)
		}
	}
	return out
}

// wantStatusCol: piped output only grows the trailing status column when a status flag or sort asks for it.
func wantStatusCol(o *Opts) bool {
	return o.Seen["status"] || o.B["working"] || o.B["idle"] || o.B["show-status"] || o.str("sort") == "status"
}

func (a *App) listRows(o *Opts, rows []row) error {
	v := a.resolveView(o, true)
	total := len(rows)
	rows = sortedBy(rows, o.str("sort"), o.B["reverse"],
		func(r row) int { return r.num }, func(r row) string { return fmt.Sprintf("%012d", r.num) },
		func(r row) string { return r.updated }, func(r row) string { return r.title },
		func(r row) int { return StatusRank(r.work.Name) })
	rows = limitRows(rows, v.limit)
	switch v.format {
	case "json":
		l := []any{}
		for _, r := range rows {
			l = append(l, Obj{{"ref", r.ref}, {"state", r.state}, {"status", r.work.Name}, {"status_detail", r.work.Label()}, {"milestone", nilIfEmpty(r.milestone)}, {"labels", splitNonEmpty(r.labels)},
				{"title", r.title}, {"updated_at", r.updated}})
		}
		a.println(dumps(l, false))
	case "table":
		if len(rows) == 0 {
			if m := a.noMirror(a.shortsFor(o)); len(m) > 0 {
				fmt.Fprintf(a.Err, "no mirror yet for %s; run moon issues pull\n", strings.Join(m, ", "))
			} else {
				fmt.Fprintln(a.Err, "no issues match (try --state all, or drop the filters)")
			}
			return nil
		}
		mode := o.str("labels")
		if mode == "" {
			mode = LabelModeFor(a.Width)
		}
		cols := []col{{head: "REF", max: 18}, {head: "STATE", max: 6}, {head: "STATUS", max: 28}}
		multi := a.multiRepo()
		if multi { // a list over several repos says which repo each row is from
			cols = append([]col{{head: "REF", max: 18}, {head: "REPO", max: 14}}, cols[1:]...)
		}
		lc := len(cols) // index of the LABELS column, when there is one
		if mode != LabelsNone {
			texts := make([]string, len(rows))
			for i, r := range rows {
				texts[i] = LabelsText(splitNonEmpty(r.labels), mode)
			}
			cols = append(cols, col{head: "LABELS", max: LabelColWidth(texts, "LABELS", a.Width)})
		}
		cols = append(cols, col{head: "TITLE", flex: true})
		cells := make([][]cell, len(rows))
		for i, r := range rows {
			c := []cell{{s: r.ref, color: cBold}}
			if multi {
				c = append(c, plain(shortOf(r.ref)))
			}
			c = append(c, cell{s: r.state, color: stateColor(r.state)}, cell{s: r.work.Label(), color: statusColor(r.work.Name)})
			if mode != LabelsNone {
				c = append(c, a.labelCell(shortOf(r.ref), splitNonEmpty(r.labels), mode, cols[lc].max))
			}
			cells[i] = append(c, plain(r.title))
		}
		fmt.Fprint(a.Out, renderTable(cols, cells, a.Width, a.styler()))
		a.footer(len(rows), total)
	default:
		withStatus := wantStatusCol(o)
		var ls []string
		for _, r := range rows {
			l := r.line
			if withStatus {
				l += "\t" + r.work.Label()
			}
			ls = append(ls, l)
		}
		a.println(strings.Join(ls, "\n"))
	}
	return nil
}

// labelCell is the labels of one row as coloured chips, whole labels only, "+N" for the ones that did not fit.
func (a *App) labelCell(short string, labels []string, mode string, w int) cell {
	shown, stored, more := FitLabelParts(labels, mode, w)
	text := FitLabels(labels, mode, w)
	if len(labels) == 0 {
		return plain("-")
	}
	colors := a.labelColors(short)
	return cell{s: text, render: func(st *Styler) string {
		var b strings.Builder
		for i, n := range shown {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(st.Chip(n, st.Pal.LabelColor(stored[i], colors[stored[i]])))
		}
		if more > 0 {
			if len(shown) > 0 {
				b.WriteString(" ")
			}
			b.WriteString(st.P(cDim, "+"+strconv.Itoa(more)))
		}
		return b.String()
	}}
}

// labelColors are the GitHub colours of one repo's labels (name to hex), from labels.tsv; empty when never pulled.
func (a *App) labelColors(short string) map[string]string {
	if m, ok := a.lblCache[short]; ok {
		return m
	}
	m := map[string]string{}
	if s, err := readText(filepath.Join(a.Home, short, "labels.tsv")); err == nil {
		for _, line := range splitLines(s) {
			if p := strings.Split(line, "\t"); len(p) >= 2 && p[1] != "" {
				m[p[0]] = p[1]
			}
		}
	}
	if a.lblCache == nil {
		a.lblCache = map[string]map[string]string{}
	}
	a.lblCache[short] = m
	return m
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// ---------- prs ----------

func (a *App) printPRs(o *Opts, rows []prItem) {
	v := a.resolveView(o, true)
	total := len(rows)
	atoi := func(it prItem) int { n, _ := strconv.Atoi(it.row.Number); return n }
	rows = sortedBy(rows, o.str("sort"), o.B["reverse"], atoi,
		func(it prItem) string { return it.row.CreatedAt }, func(it prItem) string { return it.row.UpdatedAt },
		func(it prItem) string { return it.row.Title }, nil)
	rows = limitRows(rows, v.limit)
	switch v.format {
	case "json":
		a.println(dumps(a.prJSON(rows), false))
	case "table":
		if len(rows) == 0 {
			fmt.Fprintln(a.Err, "no pull requests match (try --state all)")
			return
		}
		cells := make([][]cell, len(rows))
		multi := a.multiRepo()
		for i, it := range rows {
			st := it.row.State
			if it.row.Draft == "1" {
				st += " draft"
			}
			cells[i] = []cell{{s: it.short + "#" + it.row.Number, color: cBold}, {s: st, color: stateColor(it.row.State)}, plain(it.row.HeadRef), plain(it.row.Title)}
			if multi {
				cells[i] = append([]cell{cells[i][0], plain(it.short)}, cells[i][1:]...)
			}
		}
		cols := []col{{head: "REF", max: 18}, {head: "STATE", max: 12}, {head: "BRANCH", max: 30}, {head: "TITLE", flex: true}}
		if multi {
			cols = append([]col{cols[0], {head: "REPO", max: 14}}, cols[1:]...)
		}
		fmt.Fprint(a.Out, renderTable(cols, cells, a.Width, a.styler()))
		a.footer(len(rows), total)
	default:
		for _, it := range rows {
			r := it.row
			d := ""
			if r.Draft == "1" {
				d = " [draft]"
			}
			a.printf("%s#%s\t%s%s\t%s\t%s\n", it.short, r.Number, r.State, d, r.HeadRef, r.Title)
		}
	}
}

// ---------- claims, milestones, status ----------

// Word is the status without its detail: "working", "in review", "done (stale claim)".
func (s Status) Word() string {
	if s.Name == StDone && s.StaleClaim {
		return "done (stale claim)"
	}
	return s.Name
}

func (a *App) claimsTable(rows [][]string, sts []Status, js []any, o *Opts) {
	v := a.resolveView(o, false)
	switch v.format {
	case "json":
		a.println(dumps(js, false))
	case "table":
		if len(rows) == 0 {
			fmt.Fprintln(a.Err, "no claims")
			return
		}
		cells := make([][]cell, len(rows))
		multi := a.multiRepo()
		for i, r := range rows {
			rest := ""
			if sts[i].Name == StDone || sts[i].Name == StStale { // dead weight: dim the row
				rest = cDim
			}
			cells[i] = []cell{{s: r[0], color: cBold}, {s: sts[i].Word(), color: statusColor(sts[i].Name)}, {s: r[2], color: rest}, {s: r[3], color: rest},
				{s: r[4], color: rest}, {s: r[5], color: rest}, {s: r[6], color: rest}}
			if multi {
				cells[i] = append([]cell{cells[i][0], plain(shortOf(r[0]))}, cells[i][1:]...)
			}
		}
		cols := []col{{head: "REF"}, {head: "STATE"}, {head: "AGENT", max: 24}, {head: "HOST", max: 24}, {head: "PID"}, {head: "AGE"}, {head: "TTL"}}
		if multi {
			cols = append([]col{cols[0], {head: "REPO", max: 14}}, cols[1:]...)
		}
		fmt.Fprint(a.Out, renderTable(cols, cells, a.Width, a.styler()))
	default:
		for i, r := range rows {
			a.printf("%s\t%s\t%s\t%s\tpid=%s\tage=%ss\tttl=%ss", r[0], r[1], r[2], r[3], r[4], r[5], r[6])
			if o.B["show-status"] {
				a.printf("\t%s", sts[i].Label())
			}
			a.printf("\n")
		}
	}
}

// humanClaimRows rewrites the raw seconds of a claim row for the table.
func humanClaimRow(r []string) []string {
	age, _ := strconv.Atoi(r[5])
	ttl, _ := strconv.Atoi(r[6])
	out := append([]string{}, r...)
	out[5], out[6] = humanDur(age), humanDur(ttl)
	return out
}

// ---------- show ----------

// showPretty prints one mirrored issue for a person: a header block, then the markdown wrapped to the terminal.
func (a *App) showPretty(ref string, e *eff, h map[string]any, text string, claim string, status Status) {
	body := text
	if i := strings.Index(text, "\n---\n"); strings.HasPrefix(text, "---\n") && i >= 0 {
		body = strings.TrimLeft(text[i+5:], "\n")
	}
	w := a.Width
	a.println(a.P(cBold, ellipsize(ref+"  "+e.title, w)))
	meta := []string{a.P(stateColor(e.state), e.state), "milestone " + orDash(e.milestone)}
	if au := hStr(h, "author"); au != "" {
		meta = append(meta, "by "+au)
	}
	if u := hStr(h, "updated_at"); u != "" {
		meta = append(meta, "updated "+u)
	}
	a.println(strings.Join(meta, " · "))
	a.println(a.labelsLine(ref, e.labels, w))
	a.println("claim:  " + claim)
	a.println("status: " + a.P(statusColor(status.Name), status.Label()))
	a.println(a.P(cDim, strings.Repeat("─", min(w, 72))))
	a.println(wrapText(strings.TrimRight(body, "\n"), w, a.styler()))
}

// labelsLine lists every label in full as chips, wrapped onto more lines when they do not fit; never cut.
func (a *App) labelsLine(ref string, labels []string, w int) string {
	if len(labels) == 0 {
		return "labels: -"
	}
	const lead = "labels:"
	colors := a.labelColors(shortOf(ref))
	ordered := OrderLabels(labels)
	var lines []string
	cur, curW, n := lead, len(lead), 0 // n: labels on this line
	for i, l := range ordered {
		chip := l
		if st := a.styler(); st != nil {
			chip = st.Chip(l, st.Pal.LabelColor(l, colors[l]))
		}
		tail := ""
		if i < len(ordered)-1 {
			tail = ","
		}
		if n > 0 && curW+1+runeLen(l)+len(tail) > w {
			lines = append(lines, cur)
			cur, curW, n = strings.Repeat(" ", len(lead)), len(lead), 0
		}
		cur += " " + chip + tail
		curW += 1 + runeLen(l) + len(tail)
		n++
	}
	return strings.Join(append(lines, cur), "\n")
}
