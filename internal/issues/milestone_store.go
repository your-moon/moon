package issues

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// <mirror>/<alias>/milestones.tsv, one row per milestone, seven tab-separated fields:
//
//	number  local_id  title  state  due  description  updated
//
// number is empty for a milestone that is still local; local_id is the M-<n> it was created under
// (kept after the sync, filled from localmap.tsv by pull). due is an ISO date or empty. description is
// one field: backslash, tab, newline and carriage return are escaped. For a GitHub repo the file mirrors
// GitHub (pull rewrites it) and what is still queued is overlaid from the outbox on every read. For a
// local-only repo the file is canonical. Rows with five fields (title, state, closed, open, due) are the
// older layout; they are still read, with their stored counts, until the next pull replaces them.
// Open/closed counts are never stored: they come from the issue index.

const msColumns = 7

// msRec is one milestone before counts are attached.
type msRec struct {
	Number                           int
	LocalID, Title, State, Due, Desc string
	Updated                          string
	key                              string // the title the issues carry (differs from Title after a queued rename)
	pending                          bool   // queued locally, not on GitHub yet
	legacy                           bool   // five-field row: counts below are stored
	lClosed, lOpen                   int
}

func escField(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)
	return r.Replace(s)
}

func unescField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func (r msRec) row() string {
	num := ""
	if r.Number > 0 {
		num = strconv.Itoa(r.Number)
	}
	return strings.Join([]string{num, r.LocalID, clean(r.Title), r.State, r.Due, escField(r.Desc), r.Updated}, "\t")
}

func (a *App) msPath(short string) string { return filepath.Join(a.Home, short, "milestones.tsv") }

// readMsFile parses milestones.tsv (either layout). A missing file is no rows.
func (a *App) readMsFile(short string) []msRec {
	s, err := readText(a.msPath(short))
	if err != nil {
		return nil
	}
	var out []msRec
	for _, line := range splitLines(s) {
		f := strings.Split(line, "\t")
		if len(f) >= msColumns {
			n, _ := strconv.Atoi(f[0])
			out = append(out, msRec{Number: n, LocalID: f[1], Title: f[2], State: f[3], Due: f[4], Desc: unescField(f[5]), Updated: f[6]})
			continue
		}
		for len(f) < 5 {
			f = append(f, "")
		}
		c, _ := strconv.Atoi(f[2])
		o, _ := strconv.Atoi(f[3])
		out = append(out, msRec{Title: f[0], State: f[1], Due: f[4], legacy: true, lClosed: c, lOpen: o})
	}
	return out
}

func (a *App) writeMsFile(short string, recs []msRec) error {
	var b strings.Builder
	for _, r := range recs {
		if r.legacy { // never mixed into a new-layout file
			r.legacy = false
		}
		b.WriteString(r.row() + "\n")
	}
	return atomicWrite(a.msPath(short), b.String(), false)
}

var opMarkerRe = regexp.MustCompile(`\s*<!-- moon-op: [^>]*-->`)

// dueDate keeps the date part of a GitHub due_on timestamp.
func dueDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// msLocalIDs maps a GitHub milestone number to the M-<n> it was created under, from localmap.tsv.
func (a *App) msLocalIDs(short string) map[int]string {
	out := map[int]string{}
	s, err := readText(a.localmap())
	if err != nil {
		return out
	}
	for _, line := range strings.Split(s, "\n") {
		p := strings.Split(line, "\t")
		if len(p) == 3 && p[0] == short && strings.HasPrefix(p[1], "M-") {
			if n, err := strconv.Atoi(p[2]); err == nil {
				out[n] = p[1]
			}
		}
	}
	return out
}

// msRecs is everything known about the milestones of one alias: the file, then titles that only the
// issues mention, then (GitHub repos) the queued writes applied on top.
func (a *App) msRecs(short string) []msRec {
	recs := a.readMsFile(short)
	have := map[string]bool{}
	for _, r := range recs {
		have[r.Title] = true
	}
	closed, open, _ := a.msCounts(short)
	var extra []string
	for t := range closed {
		if !have[t] {
			extra = append(extra, t)
			have[t] = true
		}
	}
	for t := range open {
		if !have[t] {
			extra = append(extra, t)
			have[t] = true
		}
	}
	sort.Strings(extra)
	for _, t := range extra {
		st := "open"
		if open[t] == 0 {
			st = "closed"
		}
		recs = append(recs, msRec{Title: t, State: st})
	}
	for i := range recs {
		recs[i].key = recs[i].Title
	}
	if a.localOnly(short) {
		return recs
	}
	for _, op := range a.loadOutbox() {
		if isMsOp(op.Op) && shortOf(op.Ref) == short {
			recs = applyMsOp(recs, op)
		}
	}
	return recs
}

// applyMsOp shows one queued milestone write on top of recs.
func applyMsOp(recs []msRec, op *Op) []msRec {
	args := op.Args
	if op.Op == "ms_new" {
		return append(recs, msRec{LocalID: argStr(args, "local_id"), Title: argStr(args, "title"), key: argStr(args, "title"), State: "open",
			Due: argStr(args, "due"), Desc: argStr(args, "description"), Updated: op.TS, pending: true})
	}
	for i := range recs {
		r := &recs[i]
		if r.Title != argStr(args, "title") {
			continue
		}
		applyMsChange(r, op.Op, args)
		r.pending = true
		break
	}
	return recs
}

// applyMsChange mutates r the way ms_edit, ms_close and ms_reopen say.
func applyMsChange(r *msRec, op string, args map[string]any) {
	if r.key == "" {
		r.key = r.Title
	}
	if k := kindOrNil(op); k != nil {
		k.applyMs(r, args)
	}
}

// msCounts counts the issues per milestone title from the index. queued counts the issues that are
// only in the outbox (open), by the milestone they will have once everything queued is applied.
func (a *App) msCounts(short string) (closed, open, queued map[string]int) {
	closed, open, queued = map[string]int{}, map[string]int{}, map[string]int{}
	for _, r := range a.readIndex(short) {
		if r[2] == "" {
			continue
		}
		if r[1] == "closed" {
			closed[r[2]]++
		} else {
			open[r[2]]++
		}
	}
	pm := map[string]*eff{} // queued new issue -> its view with the later queued writes applied
	var order []string
	for _, op := range a.loadOutbox() {
		if shortOf(op.Ref) != short {
			continue
		}
		if op.Op == "new" {
			pm[op.Ref] = &eff{milestone: argStr(op.Args, "milestone")}
			order = append(order, op.Ref)
		} else if e, ok := pm[op.Ref]; ok {
			_ = applyOps(e, []*Op{op}) // a kind we do not know is reported by push; it changes no count
		}
	}
	for _, ref := range order {
		if t := pm[ref].milestone; t != "" {
			queued[t]++
		}
	}
	return
}

// msRows attaches counts to the records of the given aliases.
func (a *App) msRows(shorts []string) []ms {
	var out []ms
	for _, short := range shorts {
		closed, open, queued := a.msCounts(short)
		for _, r := range a.msRecs(short) {
			k := r.key
			if k == "" {
				k = r.Title
			}
			c, o := closed[k], open[k]+queued[r.Title]
			if k != r.Title {
				o += queued[k]
			}
			if r.legacy {
				c, o = r.lClosed, r.lOpen
			}
			out = append(out, ms{repo: short, title: r.Title, state: r.State, due: r.Due, closed: c, open: o,
				number: r.Number, localID: r.LocalID, desc: r.Desc, pending: r.pending,
				raw: fmt.Sprintf("%s\t%s\t%s\t%d\t%d\t%s", short, r.Title, r.State, c, o, r.Due)})
		}
	}
	return out
}

// ---------- titles, refs and repo choice ----------

var msRefRe = regexp.MustCompile(`^M-(\d+)$`)

// msTitleOfRef turns M-<n> into the milestone's title for alias. ok is false when typed is not an M- ref.
func (a *App) msTitleOfRef(short, typed string) (title string, ok bool, err error) {
	if !msRefRe.MatchString(typed) {
		return typed, false, nil
	}
	recs := a.msRecs(short)
	for _, r := range recs {
		if r.LocalID == typed {
			return r.Title, true, nil
		}
	}
	if n := a.localmapLookup(short, typed); n != 0 {
		for _, r := range recs {
			if r.Number == n {
				return r.Title, true, nil
			}
		}
	}
	return "", true, die("no milestone %s in %s (local ids look like M-1; see: moon issues milestones)", typed, short)
}

// expandMsRef rewrites --milestone M-<n> to the title, for the repo the command targets.
func (a *App) expandMsRef(o *Opts) error {
	typed := o.str("milestone")
	if !msRefRe.MatchString(typed) {
		return nil
	}
	short := ""
	if len(o.Pos) > 0 {
		short, _, _ = a.parseRef(o.Pos[0])
	}
	if repos := a.scopeRepos(); short == "" && len(repos) == 1 { // -R, the project, or the only configured repo
		short = repos[0]
	}
	if short == "" {
		return die("milestone %s is per repo: add --repo", typed)
	}
	t, _, err := a.msTitleOfRef(short, typed)
	if err != nil {
		return err
	}
	o.S["milestone"] = t
	return nil
}

// msDup finds a record whose title equals title, ignoring case, other than the one at skip (-1: none).
func msDup(recs []msRec, title string, skip string) *msRec {
	for i := range recs {
		if recs[i].Title != skip && strings.EqualFold(recs[i].Title, title) {
			return &recs[i]
		}
	}
	return nil
}

// msRepo picks the alias for a command that names no milestone: --repo, else the only configured alias.
func (a *App) msRepo(o *Opts) (string, error) {
	if repos := a.scopeRepos(); len(repos) == 1 { // -R, the project you are in, or the only configured repo
		return repos[0], nil
	}
	return "", die("which repo? pass --repo (-R) with one of: %s", strings.Join(a.Shorts(), ", "))
}

// msFindRepo finds the alias and record for a typed title or M-<n>: --repo if given, else the only
// alias that has it.
func (a *App) msFindRepo(o *Opts, typed string) (string, msRec, error) {
	shorts := a.scopeRepos()
	type hit struct {
		short string
		rec   msRec
	}
	var hits []hit
	var all, amb []string
	for _, short := range shorts {
		recs := a.msRecs(short)
		var titles []string
		for _, r := range recs {
			titles = append(titles, r.Title)
		}
		all = append(all, titles...)
		title, isRef, err := a.msTitleOfRef(short, typed)
		if isRef {
			if err != nil {
				continue
			}
		} else {
			t, ambig, ok := matchMilestone(typed, titles)
			if !ok {
				amb = append(amb, ambig...)
				continue
			}
			title = t
		}
		for _, r := range recs {
			if r.Title == title {
				hits = append(hits, hit{short, r})
				break
			}
		}
	}
	switch {
	case len(hits) == 1:
		return hits[0].short, hits[0].rec, nil
	case len(hits) > 1:
		var s []string
		for _, h := range hits {
			s = append(s, h.short)
		}
		return "", msRec{}, die("milestone '%s' exists in several repos (%s): pass --repo (-R)", typed, strings.Join(s, ", "))
	case len(amb) > 0:
		sort.Strings(amb)
		return "", msRec{}, die("milestone '%s' is ambiguous: %s", typed, strings.Join(amb, ", "))
	}
	sort.Strings(all)
	if len(all) == 0 {
		return "", msRec{}, die("no milestone '%s': there are none yet (create one: moon issues milestones new \"%s\")", typed, typed)
	}
	return "", msRec{}, die("no milestone '%s'; known milestones: %s", typed, strings.Join(all, ", "))
}

func nilIfZero(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
