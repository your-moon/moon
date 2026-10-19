package issues

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Opts is the parsed command line of one subcommand.
type Opts struct {
	S    map[string]string
	L    map[string][]string
	B    map[string]bool
	Pos  []string
	Seen map[string]bool
}

func (o *Opts) str(k string) string { return o.S[k] }

// shortsFor is the repos a command works on: the project scope, -R, or every repo (see scope.go).
func (a *App) shortsFor(o *Opts) []string { return a.scopeRepos() }

type row struct {
	ref, line                       string
	num                             int // issue number; local not-yet-pushed issues sort as newest
	state, milestone, labels, title string
	updated                         string
	local                           bool
	work                            Status
	deps                            []int // numbers the body depends on
}

func (a *App) collect(o *Opts, state, q string) []row {
	q = strings.ToLower(q)
	label, ms := o.str("label"), o.str("milestone")
	keep, _ := statusSet(o) // already validated by resolveFilters
	we := a.newWorkEnv()
	var out []row
	for _, short := range a.shortsFor(o) {
		for _, r := range a.readIndex(short) {
			num, st, rms, labels, title := r[0], r[1], r[2], r[3], r[5]
			if state != "all" && st != state {
				continue
			}
			if label != "" && !contains(strings.Split(labels, ","), label) {
				continue
			}
			if ms != "" && rms != ms {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(title), q) && !strings.Contains(strings.ToLower(labels), q) {
				continue
			}
			n, _ := strconv.Atoi(num)
			ws := we.status(short+"#"+num, short, n, st, splitNonEmpty(labels))
			if keep != nil && !keep[ws.Name] {
				continue
			}
			out = append(out, row{ref: short + "#" + num, work: ws, deps: we.deps[short+"#"+num], line: short + "#" + num + "\t" + st + "\t" + orDash(rms) + "\t" + orDash(labels) + "\t" + title,
				num: n, state: st, milestone: rms, labels: labels, title: title, updated: r[4]})
		}
		var pend []*Op
		for _, op := range a.loadOutbox() {
			if op.Op == "new" && shortOf(op.Ref) == short {
				pend = append(pend, op)
			}
		}
		sort.SliceStable(pend, func(i, j int) bool { // numeric: L-2 before L-10
			return cmpLocalIdent(pend[i].Ref[len(short)+1:], pend[j].Ref[len(short)+1:]) < 0
		})
		for _, op := range pend {
			if state == "closed" {
				continue
			}
			t := op.Args
			labels := argList(t, "labels")
			tm := argStr(t, "milestone")
			title := argStr(t, "title")
			if label != "" && !contains(labels, label) {
				continue
			}
			if ms != "" && tm != ms {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(title), q) {
				continue
			}
			ws := we.statusBody(op.Ref, short, 0, "open", labels, argStr(t, "body"))
			if keep != nil && !keep[ws.Name] {
				continue
			}
			out = append(out, row{ref: op.Ref, work: ws, deps: we.deps[op.Ref], line: op.Ref + "\topen\t" + orDash(tm) + "\t" + orDash(strings.Join(labels, ",")) + "\t" + title + " (pending sync)\tlocal",
				num: math.MaxInt32, state: "open", milestone: tm, labels: strings.Join(labels, ","), title: title + " (pending sync)",
				updated: a.Now().UTC().Format("2006-01-02T15:04:05Z"), local: true})
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// resolveFilters validates and normalises the filter flags shared by ls, frontier and milestones show.
func (a *App) resolveFilters(o *Opts) error {
	if err := a.resolveMilestone(o); err != nil {
		return err
	}
	if _, err := statusSet(o); err != nil {
		return err
	}
	return a.resolveLabel(o)
}

func (a *App) cmdLs(o *Opts) error {
	if err := a.resolveFilters(o); err != nil {
		return err
	}
	return a.listRows(o, a.collect(o, o.str("state"), o.str("q")))
}

func (a *App) cmdFrontier(o *Opts) error {
	if err := a.resolveFilters(o); err != nil {
		return err
	}
	var rows []row
	for _, r := range a.collect(o, "open", "") {
		if st, _ := a.claimStatus(r.ref); st != "fresh" {
			rows = append(rows, r)
		}
	}
	return a.listRows(o, rows)
}

type eff struct {
	title, state, milestone, body string
	labels                        []string
}

func oneLine(s string) string { return strings.ReplaceAll(pyStrip(s), "\n", " ") }

const liveNote = "(fetched live: older than %g days, not kept locally)"

func (a *App) cmdShow(o *Opts) error {
	ref := o.Pos[0]
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return err
	}
	v := a.resolveView(o, false)
	if v.format == "json" {
		return a.showJSON(ref, short, ident)
	}
	pretty := v.format == "table"
	ops := a.loadOutbox()
	nowNote := ""
	if isLocalIdent(ident) {
		n := 0
		pend := false
		for _, op := range ops {
			if op.Op == "new" && op.Ref == ref {
				pend = true
			}
		}
		if !pend {
			n = a.localmapLookup(short, ident)
		}
		if n != 0 {
			nowNote = fmt.Sprintf("ref: %s (now #%d)\n", ident, n)
			ref = short + "#" + strconv.Itoa(n)
			ident = strconv.Itoa(n)
		}
	}
	var pending []*Op
	for _, op := range ops {
		if op.Ref == ref {
			pending = append(pending, op)
		}
	}
	st, cl := a.claimStatus(ref)
	claim := "none"
	if cl != nil {
		claim = a.holderText(cl)
		if st == "stale" {
			claim += "  [STALE]"
		}
	}
	status := a.showStatus(short, ident, ref, pending)
	if !pretty || isLocalIdent(ident) {
		a.printf("claim: %s\n", claim)
		if pretty || o.B["show-status"] {
			a.printf("status: %s\n", status.Label())
		}
	}
	a.printf("%s", nowNote)
	if isLocalIdent(ident) {
		var nw *Op
		for _, op := range pending {
			if op.Op == "new" {
				nw = op
				break
			}
		}
		if nw == nil {
			return die1("no such local issue %s", ref)
		}
		t := nw.Args
		e := &eff{title: argStr(t, "title"), state: "open", labels: argList(t, "labels"), milestone: argStr(t, "milestone"), body: argStr(t, "body")}
		if err := applyOps(e, pending); err != nil {
			return err
		}
		a.printf("ref: %s (local, pending sync: not on GitHub yet)\ntitle: %s\nstate: %s\nlabels: %s\nmilestone: %s\n\n%s\n",
			ref, e.title, e.state, strings.Join(e.labels, ","), orDash(e.milestone), e.body)
	} else {
		live := false
		var text string
		var h map[string]any
		text, rerr := a.mirror().Issues.Get(short, ident)
		if rerr == nil {
			h = headerOf(text)
		} else if isNotExist(rerr) && a.localOnly(short) {
			return die1("no such issue %s", ref)
		} else if isNotExist(rerr) {
			found := false
			for _, r := range a.readIndex(short) {
				if r[0] == ident {
					found = true
				}
			}
			if !found {
				if a.TTY {
					if m := a.noMirror([]string{short}); len(m) > 0 {
						return die1("no mirror yet for %s; run moon issues pull", short)
					}
					return die1("no issue %s in the mirror; run moon issues pull", ref)
				}
				return die1("%s not in the local mirror (run: issues.py pull --repo %s)", ref, short)
			}
			n, err := a.fetchIssueLive(short, ident)
			if err != nil {
				return die1("%s is not kept locally (older than %g days) and the live fetch failed: %s", ref, a.keepDays(), err)
			}
			text = renderIssue(n)
			labels := []any{}
			for _, l := range n.Labels.Nodes {
				labels = append(labels, l.Name)
			}
			h = map[string]any{"title": n.Title, "state": strings.ToLower(n.State), "labels": labels}
			if n.Milestone != nil {
				h["milestone"] = n.Milestone.Title
			}
			live = true
		} else {
			return rerr
		}
		if h == nil {
			h = map[string]any{}
		}
		e := &eff{title: hStr(h, "title"), state: hStr(h, "state"), labels: hList(h, "labels"), milestone: hStr(h, "milestone")}
		if err := applyOps(e, pending); err != nil {
			return err
		}
		if pretty {
			if len(pending) > 0 {
				e.title += " (pending sync)"
			}
			a.showPretty(ref, e, h, text, claim, status)
			if live {
				a.printf("\n"+liveNote+"\n", a.keepDays())
			}
		} else if len(pending) > 0 {
			a.printf("PENDING SYNC: %d local op(s) not on GitHub yet; effective state: %s | labels: %s | milestone: %s | title: %s (pending sync)\n\n",
				len(pending), e.state, orDash(strings.Join(e.labels, ",")), orDash(e.milestone), e.title)
		}
		if !pretty {
			a.println(strings.TrimRight(text, "\n"))
			if live {
				a.printf("\n"+liveNote+"\n", a.keepDays())
			}
		}
	}
	if len(pending) > 0 {
		a.printf("\n## Pending sync (not yet on GitHub)\n")
		for _, op := range pending {
			if op.Op != "new" {
				s := "- (pending sync) " + describe(op)
				if op.LastError != nil && *op.LastError != "" {
					s += "  [last error: " + *op.LastError + "]"
				}
				a.println(s)
			}
		}
	}
	return nil
}

// splitLines mimics iterating a text file: no trailing empty element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func (a *App) cmdStatus(o *Opts) error {
	v := a.resolveView(o, false)
	var ops []*Op
	for _, op := range a.loadOutbox() {
		if a.inScope(shortOf(op.Ref)) {
			ops = append(ops, op)
		}
	}
	errs := 0
	for _, op := range ops {
		if op.LastError != nil && *op.LastError != "" {
			errs++
		}
	}
	type repoStat struct {
		short  string
		ago    int
		pulled bool
		n      int
		local  bool
	}
	var stats []repoStat
	for _, short := range a.scopeRepos() {
		if a.localOnly(short) {
			stats = append(stats, repoStat{short: short, n: len(a.readIndex(short)), local: true})
			continue
		}
		s, err := a.mirror().Issues.Read(short, fileLastpull)
		t, perr := strconv.Atoi(pyStrip(s))
		if err != nil || perr != nil {
			stats = append(stats, repoStat{short: short})
			continue
		}
		stats = append(stats, repoStat{short: short, ago: int(a.Now().Unix()) - t, pulled: true, n: len(a.readIndex(short))})
	}
	switch v.format {
	case "json":
		rs := []any{}
		for _, r := range stats {
			var ago any
			if r.pulled {
				ago = r.ago
			}
			rs = append(rs, Obj{{"repo", r.short}, {"last_pull_seconds", ago}, {"issues", r.n}, {"local_only", r.local}})
		}
		e := []any{}
		for _, op := range ops {
			if op.LastError != nil && *op.LastError != "" {
				e = append(e, Obj{{"op_id", op.OpID}, {"op", op.Op}, {"ref", op.Ref}, {"attempts", op.Attempts}, {"error", *op.LastError}})
			}
		}
		ss := a.SyncStatus()
		a.println(dumps(Obj{{"outbox", len(ops)}, {"errors", errs}, {"error_ops", e}, {"repos", rs},
			{"autosync", Obj{{"on", !ss.Off}, {"off_reason", nilIfEmpty(ss.Why)}, {"last_run", nilIfEmpty(ss.Last)}}}}, false))
	case "table":
		c := a.P("state-open", "0 errors")
		if errs > 0 {
			c = a.P("error", fmt.Sprintf("%d errors", errs))
		}
		a.printf("outbox: %d queued, %s\n", len(ops), c)
		a.println(a.SyncStatus().Summary())
		for _, op := range ops {
			if op.LastError != nil && *op.LastError != "" {
				a.println("  " + a.P("error", "ERR") + fmt.Sprintf(" %s %s %s (attempts %d): %s", op.OpID, op.Op, op.Ref, op.Attempts, ellipsize(*op.LastError, max(a.Width-40, 20))))
			}
		}
		for _, r := range stats {
			if r.local {
				a.printf("%s: local-only, %d issues\n", r.short, r.n)
			} else if !r.pulled {
				a.printf("%s: %s, run moon issues pull\n", r.short, a.P("warning", "never pulled"))
			} else {
				a.printf("%s: last pull %s ago, %d issues\n", r.short, humanDur(r.ago), r.n)
			}
		}
	default:
		a.printf("outbox=%d errors=%d\n", len(ops), errs)
		a.println(a.SyncStatus().Summary())
		for _, op := range ops {
			if op.LastError != nil && *op.LastError != "" {
				a.printf("  ERR %s %s %s attempts=%d: %s\n", op.OpID, op.Op, op.Ref, op.Attempts, trunc(*op.LastError, 200))
			}
		}
		for _, r := range stats {
			if r.local {
				a.printf("%s: local-only, issues=%d\n", r.short, r.n)
				continue
			}
			if !r.pulled {
				a.printf("%s: never pulled\n", r.short)
				continue
			}
			a.printf("%s: last pull %ds ago, issues=%d\n", r.short, r.ago, r.n)
		}
	}
	return nil
}

type prItem struct {
	short string
	row   PRRow
}

func (a *App) prRows(o *Opts) []prItem {
	state, since := o.str("state"), o.str("since")
	var out []prItem
	for _, short := range a.shortsFor(o) {
		for _, r := range a.readPRs(short) {
			if state != "all" && r.State != state {
				continue
			}
			ev := r.UpdatedAt
			if state == "merged" && r.MergedAt != "" {
				ev = r.MergedAt
			}
			if since != "" && ev < since { // M_since
				continue
			}
			out = append(out, prItem{short, r})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].short != out[j].short {
			return out[i].short < out[j].short
		}
		x, _ := strconv.Atoi(out[i].row.Number)
		y, _ := strconv.Atoi(out[j].row.Number)
		return x > y
	})
	return out
}

func (a *App) prJSON(rows []prItem) []any {
	l := []any{}
	for _, it := range rows {
		r := it.row
		n, _ := strconv.Atoi(r.Number)
		l = append(l, Obj{{"number", r.Number}, {"state", r.State}, {"draft", r.Draft == "1"}, {"head_ref", r.HeadRef}, {"head_oid", r.HeadOid},
			{"base_ref", r.BaseRef}, {"created_at", r.CreatedAt}, {"merged_at", r.MergedAt}, {"merge_commit", r.MergeCommit}, {"author", r.Author},
			{"updated_at", r.UpdatedAt}, {"closes", r.closesList()}, {"title", r.Title}, {"ref", it.short + "#" + r.Number}, {"repo", it.short},
			{"url", "https://github.com/" + a.repoFull(it.short) + "/pull/" + strconv.Itoa(n)}})
	}
	return l
}

func (a *App) cmdPRs(o *Opts) error {
	a.printPRs(o, a.prRows(o))
	return nil
}

func (a *App) cmdPRShow(o *Opts) error {
	ref := o.Pos[0]
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return err
	}
	if isLocalIdent(ident) {
		return die("%s is a local issue ref, not a PR", ref)
	}
	s, rerr := a.mirror().Issues.PR(short, ident)
	if rerr == nil {
		a.println(strings.TrimRight(s, "\n"))
		return nil
	}
	if _, ok := a.readPRs(short)[ident]; ok {
		n, _ := strconv.Atoi(ident)
		pr, err := a.GH.PullRequest(a.repoFull(short), n)
		if err != nil || pr == nil {
			if err == nil {
				err = &GhError{Msg: "no such PR"}
			}
			return die1("%s is not kept locally (older than %g days) and the live fetch failed: %s", ref, a.keepDays(), err)
		}
		a.printf("%s\n\n"+liveNote+"\n", strings.TrimRight(renderPR(pr, prRowOf(pr)), "\n"), a.keepDays())
		return nil
	}
	if a.TTY {
		if m := a.noMirror([]string{short}); len(m) > 0 {
			return die1("no mirror yet for %s; run moon issues pull", short)
		}
		return die1("no pull request %s in the mirror; run moon issues pull (PRs older than the first pull need --full)", ref)
	}
	return die1("%s not in the PR mirror (run: issues.py pull --repo %s; PRs older than the first pull need --full)", ref, short)
}

func (a *App) cmdClaims(o *Opts) error {
	m := a.mirror()
	names := m.Claims.List()
	var rows [][]string
	var sts []Status
	we := a.newWorkEnv()
	for _, n := range names {
		c := a.readClaim(n)
		if c == nil {
			continue
		}
		if !a.inScope(shortOf(strings.Replace(strings.TrimSuffix(n, ".json"), "-", "#", 1))) {
			continue
		}
		st := "fresh"
		if a.claimIsStale(c) {
			st = "STALE"
		}
		cref := strings.Replace(strings.TrimSuffix(n, ".json"), "-", "#", 1)
		sts = append(sts, a.claimWork(we, cref))
		pid := "-"
		if c.Pid != 0 {
			pid = strconv.Itoa(c.Pid)
		}
		rows = append(rows, []string{strings.Replace(strings.TrimSuffix(n, ".json"), "-", "#", 1), st, c.Agent, c.Host, pid,
			strconv.Itoa(int(a.nowEpoch() - c.TS)), strconv.Itoa(int(c.TTL))})
	}
	v := a.resolveView(o, false)
	js := []any{}
	for _, r := range rows {
		age, _ := strconv.Atoi(r[5])
		ttl, _ := strconv.Atoi(r[6])
		js = append(js, Obj{{"ref", r[0]}, {"state", strings.ToLower(r[1])}, {"status", sts[len(js)].Name}, {"issue_closed", sts[len(js)].Name == StDone}, {"agent", r[2]}, {"host", r[3]}, {"pid", r[4]}, {"age_seconds", age}, {"ttl_seconds", ttl}})
	}
	if v.format == "table" {
		for i := range rows {
			rows[i] = humanClaimRow(rows[i])
		}
	}
	a.claimsTable(rows, sts, js, o)
	return nil
}

// issueFacts is the effective state and labels of an issue as the mirror (plus queued ops) has it; an issue
// the mirror does not have counts as open with no labels.
func (a *App) issueFacts(short, ident string, pending []*Op) (state string, labels []string) {
	e := &eff{state: "open"}
	if isLocalIdent(ident) {
		for _, op := range a.loadOutbox() {
			if op.Op == "new" && op.Ref == short+"#"+ident {
				e.labels = argList(op.Args, "labels")
			}
		}
	} else {
		for _, r := range a.readIndex(short) {
			if r[0] == ident {
				e.state, e.labels = r[1], splitNonEmpty(r[3])
			}
		}
	}
	applyPending(e, pending)
	return e.state, e.labels
}

func (a *App) showStatus(short, ident, ref string, pending []*Op) Status {
	state, labels := a.issueFacts(short, ident, pending)
	n, _ := strconv.Atoi(ident)
	return a.newWorkEnv().status(ref, short, n, state, labels)
}

// claimWork is the status of the issue a claim file belongs to.
func (a *App) claimWork(we *workEnv, ref string) Status {
	short, ident, _ := strings.Cut(ref, "#")
	state, labels := a.issueFacts(short, ident, nil)
	n, _ := strconv.Atoi(ident)
	return we.status(ref, short, n, state, labels)
}
