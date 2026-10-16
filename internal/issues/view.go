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
		if a.TTY {
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
func sortedBy[T any](items []T, key string, reverse bool, num func(T) int, created, updated, title func(T) string) []T {
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

func (a *App) claimCell(ref string) cell {
	switch st, c := a.claimStatus(ref); st {
	case "fresh":
		return cell{c.Agent, cYellow}
	case "stale":
		return cell{c.Agent + " (stale)", cRed}
	}
	return cell{"-", cDim}
}

func (a *App) listRows(o *Opts, rows []row) error {
	v := a.resolveView(o, true)
	total := len(rows)
	rows = sortedBy(rows, o.str("sort"), o.B["reverse"],
		func(r row) int { return r.num }, func(r row) string { return fmt.Sprintf("%012d", r.num) },
		func(r row) string { return r.updated }, func(r row) string { return r.title })
	rows = limitRows(rows, v.limit)
	switch v.format {
	case "json":
		l := []any{}
		for _, r := range rows {
			l = append(l, Obj{{"ref", r.ref}, {"state", r.state}, {"milestone", nilIfEmpty(r.milestone)}, {"labels", splitNonEmpty(r.labels)},
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
		cells := make([][]cell, len(rows))
		for i, r := range rows {
			cells[i] = []cell{plain(r.ref), {r.state, stateColor(r.state)}, a.claimCell(r.ref), plain(shortLabels(orDash(r.labels))), plain(r.title)}
		}
		fmt.Fprint(a.Out, renderTable([]col{{head: "REF", max: 18}, {head: "STATE", max: 6}, {head: "CLAIM", max: 16},
			{head: "LABELS", max: 28}, {head: "TITLE", flex: true}}, cells, a.Width, a.Color))
		a.footer(len(rows), total)
	default:
		var ls []string
		for _, r := range rows {
			ls = append(ls, r.line)
		}
		a.println(strings.Join(ls, "\n"))
	}
	return nil
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
		func(it prItem) string { return it.row.Title })
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
		for i, it := range rows {
			st := it.row.State
			if it.row.Draft == "1" {
				st += " draft"
			}
			cells[i] = []cell{plain(it.short + "#" + it.row.Number), {st, stateColor(it.row.State)}, plain(it.row.HeadRef), plain(it.row.Title)}
		}
		fmt.Fprint(a.Out, renderTable([]col{{head: "REF", max: 18}, {head: "STATE", max: 12}, {head: "BRANCH", max: 30}, {head: "TITLE", flex: true}},
			cells, a.Width, a.Color))
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

func (a *App) claimsTable(rows [][]string, js []any, o *Opts) {
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
		for i, r := range rows {
			col := cGreen
			if r[1] == "STALE" {
				col = cRed
			}
			cells[i] = []cell{plain(r[0]), {strings.ToLower(r[1]), col}, plain(r[2]), plain(r[3]), plain(r[4]), plain(r[5]), plain(r[6])}
		}
		fmt.Fprint(a.Out, renderTable([]col{{head: "REF"}, {head: "STATE"}, {head: "AGENT", max: 24}, {head: "HOST", max: 24}, {head: "PID"},
			{head: "AGE"}, {head: "TTL"}}, cells, a.Width, a.Color))
	default:
		for _, r := range rows {
			a.printf("%s\t%s\t%s\t%s\tpid=%s\tage=%ss\tttl=%ss\n", r[0], r[1], r[2], r[3], r[4], r[5], r[6])
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
func (a *App) showPretty(ref string, e *eff, h map[string]any, text string, claim string) {
	body := text
	if i := strings.Index(text, "\n---\n"); strings.HasPrefix(text, "---\n") && i >= 0 {
		body = strings.TrimLeft(text[i+5:], "\n")
	}
	w := a.Width
	a.println(paint(a.Color, cBold, ellipsize(ref+"  "+e.title, w)))
	meta := []string{paint(a.Color, stateColor(e.state), e.state), "milestone " + orDash(e.milestone)}
	if au := hStr(h, "author"); au != "" {
		meta = append(meta, "by "+au)
	}
	if u := hStr(h, "updated_at"); u != "" {
		meta = append(meta, "updated "+u)
	}
	a.println(strings.Join(meta, " · "))
	a.println("labels: " + orDash(strings.Join(e.labels, ", ")))
	a.println("claim:  " + claim)
	a.println(paint(a.Color, cDim, strings.Repeat("─", min(w, 72))))
	a.println(wrapText(strings.TrimRight(body, "\n"), w, a.Color))
}
