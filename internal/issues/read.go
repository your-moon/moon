package issues

import (
	"os"
	"path/filepath"
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

func (a *App) shortsFor(o *Opts) []string {
	if r := o.str("repo"); r != "" {
		return []string{r}
	}
	return a.Shorts()
}

type row struct{ ref, line string }

func (a *App) collect(o *Opts, state, q string) []row {
	q = strings.ToLower(q)
	label, ms := o.str("label"), o.str("milestone")
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
			out = append(out, row{short + "#" + num, short + "#" + num + "\t" + st + "\t" + orDash(rms) + "\t" + orDash(labels) + "\t" + title})
		}
		for _, op := range a.loadOutbox() {
			if op.Op != "new" || shortOf(op.Ref) != short || state == "closed" {
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
			out = append(out, row{op.Ref, op.Ref + "\topen\t" + orDash(tm) + "\t" + orDash(strings.Join(labels, ",")) + "\t" + title + " (pending sync)"})
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

func (a *App) cmdLs(o *Opts) error {
	var ls []string
	for _, r := range a.collect(o, o.str("state"), o.str("q")) {
		ls = append(ls, r.line)
	}
	a.println(strings.Join(ls, "\n"))
	return nil
}

func (a *App) cmdFrontier(o *Opts) error {
	var ls []string
	for _, r := range a.collect(o, "open", "") {
		if st, _ := a.claimStatus(r.ref); st != "fresh" {
			ls = append(ls, r.line)
		}
	}
	a.println(strings.Join(ls, "\n"))
	return nil
}

type eff struct {
	title, state, milestone, body string
	labels                        []string
}

func applyOps(e *eff, ops []*Op) {
	for _, o := range ops {
		switch o.Op {
		case "close":
			e.state = "closed"
		case "reopen":
			e.state = "open"
		case "label":
			rm, add := argList(o.Args, "remove"), argList(o.Args, "add")
			var nl []string
			for _, l := range e.labels {
				if !contains(rm, l) {
					nl = append(nl, l)
				}
			}
			for _, l := range add {
				if !contains(e.labels, l) {
					nl = append(nl, l)
				}
			}
			e.labels = nl
		case "milestone":
			if t := argStr(o.Args, "title"); t == "none" {
				e.milestone = ""
			} else {
				e.milestone = t
			}
		case "edit":
			if t, ok := o.Args["title"].(string); ok {
				e.title = t
			}
			if t, ok := o.Args["body"].(string); ok {
				e.body = t
			}
		}
	}
}

func oneLine(s string) string { return strings.ReplaceAll(pyStrip(s), "\n", " ") }

func describe(o *Op) string {
	switch o.Op {
	case "comment":
		return "comment [" + o.OpID + "]: " + trunc(oneLine(argStr(o.Args, "body")), 300)
	case "close":
		reason := argStr(o.Args, "reason")
		if reason == "" {
			reason = "completed"
		}
		s := "close (" + reason + ")"
		if c := argStr(o.Args, "comment"); c != "" {
			s += " + comment: " + c
		}
		return s
	case "label":
		return "label +" + strings.Join(argList(o.Args, "add"), ",") + " -" + strings.Join(argList(o.Args, "remove"), ",")
	case "milestone":
		return "milestone -> " + argStr(o.Args, "title")
	case "edit":
		var p []string
		for _, x := range []string{"title", "body"} {
			if _, ok := o.Args[x]; ok {
				p = append(p, x)
			}
		}
		return "edit " + strings.Join(p, ", ")
	}
	return o.Op
}

const liveNote = "(fetched live: older than %g days, not kept locally)"

func (a *App) cmdShow(o *Opts) error {
	ref := o.Pos[0]
	short, ident, err := a.parseRef(ref)
	if err != nil {
		return err
	}
	ops := a.loadOutbox()
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
	if cl != nil {
		s := a.holderText(cl)
		if st == "stale" {
			s += "  [STALE]"
		}
		a.printf("claim: %s\n", s)
	} else {
		a.printf("claim: none\n")
	}
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
		var rest []*Op
		for _, op := range pending {
			if op.Op != "new" {
				rest = append(rest, op)
			}
		}
		applyOps(e, rest)
		a.printf("ref: %s (pending sync: not on GitHub yet)\ntitle: %s\nstate: %s\nlabels: %s\nmilestone: %s\n\n%s\n",
			ref, e.title, e.state, strings.Join(e.labels, ","), orDash(e.milestone), e.body)
	} else {
		p := filepath.Join(a.Home, short, ident+".md")
		live := false
		var text string
		var h map[string]any
		text, rerr := readText(p)
		if rerr == nil {
			h = readHeader(p)
		} else if isNotExist(rerr) {
			found := false
			for _, r := range a.readIndex(short) {
				if r[0] == ident {
					found = true
				}
			}
			if !found {
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
		applyOps(e, pending)
		if len(pending) > 0 {
			a.printf("PENDING SYNC: %d local op(s) not on GitHub yet; effective state: %s | labels: %s | milestone: %s | title: %s (pending sync)\n\n",
				len(pending), e.state, orDash(strings.Join(e.labels, ",")), orDash(e.milestone), e.title)
		}
		a.println(strings.TrimRight(text, "\n"))
		if live {
			a.printf("\n"+liveNote+"\n", a.keepDays())
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

func (a *App) cmdMilestones(o *Opts) error {
	for _, short := range a.shortsFor(o) {
		s, err := readText(filepath.Join(a.Home, short, "milestones.tsv"))
		if err != nil {
			continue
		}
		for _, line := range splitLines(s) {
			a.printf("%s\t%s\n", short, line)
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
	ops := a.loadOutbox()
	errs := 0
	for _, op := range ops {
		if op.LastError != nil && *op.LastError != "" {
			errs++
		}
	}
	a.printf("outbox=%d errors=%d\n", len(ops), errs)
	for _, op := range ops {
		if op.LastError != nil && *op.LastError != "" {
			a.printf("  ERR %s %s %s attempts=%d: %s\n", op.OpID, op.Op, op.Ref, op.Attempts, trunc(*op.LastError, 200))
		}
	}
	for _, short := range a.Shorts() {
		s, err := readText(filepath.Join(a.Home, short, "lastpull"))
		t, perr := strconv.Atoi(pyStrip(s))
		if err != nil || perr != nil {
			a.printf("%s: never pulled\n", short)
			continue
		}
		a.printf("%s: last pull %ds ago, issues=%d\n", short, int(a.Now().Unix())-t, len(a.readIndex(short)))
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

func (a *App) cmdPRs(o *Opts) error {
	rows := a.prRows(o)
	if o.B["json"] {
		l := []any{}
		for _, it := range rows {
			r := it.row
			n, _ := strconv.Atoi(r.Number)
			l = append(l, Obj{{"number", r.Number}, {"state", r.State}, {"draft", r.Draft == "1"}, {"head_ref", r.HeadRef}, {"head_oid", r.HeadOid},
				{"base_ref", r.BaseRef}, {"created_at", r.CreatedAt}, {"merged_at", r.MergedAt}, {"merge_commit", r.MergeCommit}, {"author", r.Author},
				{"updated_at", r.UpdatedAt}, {"closes", r.closesList()}, {"title", r.Title}, {"ref", it.short + "#" + r.Number}, {"repo", it.short},
				{"url", "https://github.com/" + a.repoFull(it.short) + "/pull/" + strconv.Itoa(n)}})
		}
		a.println(dumps(l, false))
		return nil
	}
	for _, it := range rows {
		r := it.row
		d := ""
		if r.Draft == "1" {
			d = " [draft]"
		}
		a.printf("%s#%s\t%s%s\t%s\t%s\n", it.short, r.Number, r.State, d, r.HeadRef, r.Title)
	}
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
	s, rerr := readText(filepath.Join(a.Home, short, "pr", ident+".md"))
	if rerr == nil {
		a.println(strings.TrimRight(s, "\n"))
		return nil
	}
	if _, ok := a.readPRs(short)[ident]; ok {
		owner, name, _ := strings.Cut(a.repoFull(short), "/")
		n, _ := strconv.Atoi(ident)
		var d struct {
			Repository struct {
				PullRequest *prNode `json:"pullRequest"`
			} `json:"repository"`
		}
		if err := a.gql(qPROne, map[string]any{"owner": owner, "name": name, "num": n}, &d); err != nil || d.Repository.PullRequest == nil {
			if err == nil {
				err = &GhError{Msg: "no such PR"}
			}
			return die1("%s is not kept locally (older than %g days) and the live fetch failed: %s", ref, a.keepDays(), err)
		}
		pr := d.Repository.PullRequest
		a.printf("%s\n\n"+liveNote+"\n", strings.TrimRight(renderPR(pr, prRowOf(pr)), "\n"), a.keepDays())
		return nil
	}
	return die1("%s not in the PR mirror (run: issues.py pull --repo %s; PRs older than the first pull need --full)", ref, short)
}

func (a *App) cmdClaims(o *Opts) error {
	ents, _ := os.ReadDir(a.claimsDir())
	names := []string{}
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		c := a.readClaim(filepath.Join(a.claimsDir(), n))
		if c == nil {
			continue
		}
		st := "fresh"
		if a.claimIsStale(c) {
			st = "STALE"
		}
		pid := "-"
		if c.Pid != 0 {
			pid = strconv.Itoa(c.Pid)
		}
		a.printf("%s\t%s\t%s\t%s\tpid=%s\tage=%ds\tttl=%ds\n", strings.Replace(strings.TrimSuffix(n, ".json"), "-", "#", 1), st, c.Agent, c.Host, pid, int(a.nowEpoch()-c.TS), int(c.TTL))
	}
	return nil
}
