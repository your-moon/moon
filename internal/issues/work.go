package issues

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Work status: what is happening to an issue right now, derived from the issue (state, labels), its claim
// and the open pull requests in the mirror. Nothing is stored; it is computed on every read, so it cannot go
// stale the way a lingering claim file does.

// Status names, in the order `-s status` sorts them (most in need of attention first).
const (
	StStale   = "stale"
	StWorking = "working"
	StReview  = "in review"
	StBlocked = "blocked"
	StReady   = "ready"
	StOpen    = "open"
	StDone    = "done"
)

// StatusOrder is the sort order and the list of valid names for --status.
var StatusOrder = []string{StStale, StWorking, StReview, StBlocked, StReady, StOpen, StDone}

// StatusRank is the position of a status name in StatusOrder.
func StatusRank(name string) int {
	for i, n := range StatusOrder {
		if n == name {
			return i
		}
	}
	return len(StatusOrder)
}

// Status is the derived work state of one issue.
type Status struct {
	Name       string // one of StatusOrder
	Agent      string // working, stale: who holds the claim
	AgeSec     int    // working, stale: how long ago the claim was taken
	PR         int    // in review: the open pull request
	StaleClaim bool   // done with a claim still on file: the claim should be released
	By         []int  // blocked: the open issues it depends on
}

// Label is the text shown in tables and TSV: "working: sonnet-17 11m", "in review: PR #588", "done (stale claim)".
func (s Status) Label() string {
	switch s.Name {
	case StWorking, StStale:
		if s.Agent != "" {
			return s.Name + ": " + s.Agent + " " + humanDur(s.AgeSec)
		}
	case StReview:
		if s.PR > 0 {
			return s.Name + ": PR #" + strconv.Itoa(s.PR)
		}
	case StBlocked:
		if len(s.By) > 0 {
			return "blocked: by " + hashList(s.By)
		}
	case StDone:
		if s.StaleClaim {
			return "done (stale claim)"
		}
	}
	return s.Name
}

func statusColor(name string) string {
	switch name {
	case StWorking:
		return "status-working"
	case StReview:
		return "status-review"
	case StStale:
		return "status-stale"
	case StBlocked:
		return "status-blocked"
	case StReady:
		return "status-ready"
	case StDone:
		return "status-done"
	}
	return ""
}

// WorkIssue is the part of an issue that decides its status.
type WorkIssue struct {
	Alias  string // repo alias
	Repo   string // owner/name, to recognise "owner/name#N" in a pull request
	Num    int    // 0 for a local issue that is not on GitHub yet
	State  string // open or closed
	Labels []string
	// OpenDeps are the open issues this one depends on (see deps.go); any of them makes it blocked.
	OpenDeps []int
}

// WorkPR is an open pull request as far as status is concerned.
type WorkPR struct {
	Number int
	Closes []int  // numbers GitHub says it closes, in the same repo
	Text   string // title and body, searched for "Closes #N" and the like
}

// WorkCtx is everything outside the issue that status depends on.
type WorkCtx struct {
	Now   time.Time
	Host  string         // this machine; a pid is only checked on the host that wrote the claim
	Alive func(int) bool // is this pid running (nil: assume yes)
}

// claimExpired is the one definition of a stale claim: past its TTL, or its process (on this host) is gone.
func claimExpired(c *Claim, ctx WorkCtx) bool {
	if float64(ctx.Now.UnixNano())/1e9-c.TS > c.TTL { // M_ttl
		return true
	}
	return c.Pid != 0 && c.Host == ctx.Host && ctx.Alive != nil && !ctx.Alive(c.Pid) // M_pid
}

// workInput is what a rule looks at.
type workInput struct {
	is      WorkIssue
	claim   *Claim
	expired bool
	pr      int
	ctx     WorkCtx
}

func hashList(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

func (w *workInput) age() int { return int(float64(w.ctx.Now.UnixNano())/1e9 - w.claim.TS) }

// Rule is one row of the priority table: the first rule whose When is true decides the status.
type Rule struct {
	Name string
	When func(w *workInput) bool
	Make func(w *workInput) Status
}

// WorkRules is the ordered rule table. Reorder it to change which status wins.
var WorkRules = []Rule{
	{StDone, func(w *workInput) bool { return w.is.State == "closed" },
		func(w *workInput) Status { return Status{Name: StDone, StaleClaim: w.claim != nil} }},
	{StReview, func(w *workInput) bool { return w.pr > 0 },
		func(w *workInput) Status { return Status{Name: StReview, PR: w.pr} }},
	{StWorking, func(w *workInput) bool { return w.claim != nil && !w.expired },
		func(w *workInput) Status { return Status{Name: StWorking, Agent: w.claim.Agent, AgeSec: w.age()} }},
	{StStale, func(w *workInput) bool { return w.claim != nil && w.expired },
		func(w *workInput) Status { return Status{Name: StStale, Agent: w.claim.Agent, AgeSec: w.age()} }},
	{StBlocked, func(w *workInput) bool { return len(w.is.OpenDeps) > 0 || hasBlockLabel(w.is.Labels) },
		func(w *workInput) Status { return Status{Name: StBlocked, By: w.is.OpenDeps} }},
	{StReady, func(w *workInput) bool { return hasReadyLabel(w.is.Labels) },
		func(w *workInput) Status { return Status{Name: StReady} }},
	{StOpen, func(w *workInput) bool { return true },
		func(w *workInput) Status { return Status{Name: StOpen} }},
}

func hasBlockLabel(labels []string) bool {
	for _, l := range labels {
		l = strings.ToLower(l)
		if strings.Contains(l, "blocked") || strings.Contains(l, "needs-decision") || strings.Contains(l, "needs-information") {
			return true
		}
	}
	return false
}

func hasReadyLabel(labels []string) bool {
	for _, l := range labels {
		l = strings.ToLower(l)
		if l == "ready" || strings.HasSuffix(l, ":ready") {
			return true
		}
	}
	return false
}

// WorkStatus derives the status of one issue. claim is nil when there is none; prs are the open pull
// requests of the same repo. It reads no files and runs no processes except through ctx.Alive.
func WorkStatus(is WorkIssue, claim *Claim, prs []WorkPR, ctx WorkCtx) Status {
	w := &workInput{is: is, claim: claim, ctx: ctx, pr: reviewPR(is, prs)}
	if claim != nil {
		w.expired = claimExpired(claim, ctx)
	}
	for _, r := range WorkRules {
		if r.When(w) {
			return r.Make(w)
		}
	}
	return Status{Name: StOpen}
}

// ---------- which pull request is about an issue ----------

var closeRefRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s*:?\s+(?:([a-z0-9_.-]+(?:/[a-z0-9_.-]+)?)#(\d+)\b|#(\d+)\b|https?://github\.com/([a-z0-9_.-]+/[a-z0-9_.-]+)/issues/(\d+)\b)`)

// ClosingRefs returns the issue numbers of this repo that text closes: "Closes #N", "fixes alias#N",
// "Resolved owner/name#N" (any case), or a link to one. References to other repos are ignored.
// The branch name is deliberately not an input.
func ClosingRefs(text, alias, repo string) []int {
	var out []int
	for _, m := range closeRefRe.FindAllStringSubmatch(text, -1) {
		var num, scope string
		switch {
		case m[3] != "":
			num = m[3]
		case m[2] != "":
			num, scope = m[2], m[1]
		default:
			num, scope = m[5], m[4]
		}
		if scope != "" && !strings.EqualFold(scope, alias) && !strings.EqualFold(scope, repo) {
			continue
		}
		if n, err := strconv.Atoi(num); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// reviewPR is the number of the open PR that references the issue (the newest if several), or 0.
func reviewPR(is WorkIssue, prs []WorkPR) int {
	if is.Num <= 0 {
		return 0
	}
	best := 0
	for _, p := range prs {
		hit := false
		for _, n := range p.Closes {
			hit = hit || n == is.Num
		}
		for _, n := range ClosingRefs(p.Text, is.Alias, is.Repo) {
			hit = hit || n == is.Num
		}
		if hit && p.Number > best {
			best = p.Number
		}
	}
	return best
}

// ---------- gathering the inputs from the mirror ----------

// workEnv reads the claims and open PRs once and answers status questions for any number of issues.
type workEnv struct {
	a      *App
	claims map[string]*Claim // by ref, e.g. app#12
	prs    map[string][]WorkPR
	ctx    WorkCtx

	deps       map[string][]int        // by ref: every number its body depends on, open or not
	depOpen    map[string]map[int]bool // by repo: is issue N open (deps.go)
	ops        []*Op
	outboxRead bool
}

func (a *App) newWorkEnv() *workEnv {
	w := &workEnv{a: a, claims: map[string]*Claim{}, deps: map[string][]int{}, prs: map[string][]WorkPR{},
		ctx: a.workCtx()}
	m := a.mirror()
	for _, name := range m.claimNames() {
		short, ident, ok := strings.Cut(strings.TrimSuffix(name, ".json"), "-")
		if !ok {
			continue
		}
		if c := a.readClaim(m.claimNamePath(name)); c != nil {
			w.claims[short+"#"+ident] = c
		}
	}
	return w
}

func (w *workEnv) openPRs(short string) []WorkPR {
	if l, ok := w.prs[short]; ok {
		return l
	}
	var l []WorkPR
	for _, r := range w.a.readPRs(short) {
		if r.State != "open" {
			continue
		}
		n, _ := strconv.Atoi(r.Number)
		text := r.Title
		if s, err := w.a.mirror().readPRText(short, r.Number); err == nil {
			text += "\n" + stripFrontMatter(s)
		}
		l = append(l, WorkPR{Number: n, Closes: r.closesList(), Text: text})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Number < l[j].Number })
	w.prs[short] = l
	return l
}

func (w *workEnv) issue(short string, num int, state string, labels []string) WorkIssue {
	return WorkIssue{Alias: short, Repo: w.a.repoFull(short), Num: num, State: state, Labels: labels}
}

// status is the work status of one issue ref with the given effective state and labels.
func (w *workEnv) status(ref, short string, num int, state string, labels []string) Status {
	body := ""
	if num > 0 && state != "closed" {
		body = w.mirrorBody(short, num)
	}
	return w.statusBody(ref, short, num, state, labels, body)
}

// statusBody is status for a caller that already has the effective body (queued edits applied, or a queued
// `new`), which is where the Depends-on line is read from.
func (w *workEnv) statusBody(ref, short string, num int, state string, labels []string, body string) Status {
	var prs []WorkPR
	if num > 0 && state != "closed" {
		prs = w.openPRs(short)
	}
	is := w.issue(short, num, state, labels)
	if all := dependsOn(body); len(all) > 0 {
		w.deps[ref] = all
	}
	if state != "closed" {
		is.OpenDeps = w.openDeps(short, body)
	}
	return WorkStatus(is, w.claims[ref], prs, w.ctx)
}

// statusSet parses --status, -w and -i into the set of status names to keep; nil means no filter.
func statusSet(o *Opts) (map[string]bool, error) {
	set := map[string]bool{}
	if o.B["working"] {
		set[StWorking] = true
	}
	if o.B["idle"] {
		set[StReady], set[StOpen] = true, true
	}
	for _, part := range strings.Split(o.str("status"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := normStatus(part)
		if name == "" {
			return nil, die("usage: moon issues: error: argument --status: invalid status '%s' (choose from %s; several with commas)", part, strings.Join(StatusOrder, ", "))
		}
		set[name] = true
	}
	if len(set) == 0 {
		return nil, nil
	}
	return set, nil
}

// normStatus maps what a person types (in-review, review, WORKING) to a status name, "" if unknown.
func normStatus(s string) string {
	s = strings.ToLower(strings.NewReplacer("-", " ", "_", " ").Replace(strings.TrimSpace(s)))
	if s == "review" {
		return StReview
	}
	for _, n := range StatusOrder {
		if n == s {
			return n
		}
	}
	return ""
}

// ---------- label matching ----------

// labelUniverse is every label on the issues of the repos in scope, plus those of queued new issues.
func (a *App) labelUniverse(o *Opts) []string {
	seen := map[string]bool{}
	for _, short := range a.shortsFor(o) {
		for _, r := range a.readIndex(short) {
			for _, l := range splitNonEmpty(r[3]) {
				seen[l] = true
			}
		}
	}
	for _, op := range a.loadOutbox() {
		if op.Op == "new" {
			for _, l := range argList(op.Args, "labels") {
				seen[l] = true
			}
		}
	}
	return sortedKeys(seen)
}

// matchLabel resolves what was typed to stored labels: the stored name itself wins; otherwise the shown short
// name (kind:bug is shown as bug) or any suffix after a colon (triage:ready is "ready", area::auth is "auth").
func matchLabel(want string, universe []string) []string {
	for _, l := range universe {
		if l == want {
			return []string{l}
		}
	}
	var out []string
	for _, l := range universe {
		lw, c := strings.ToLower(want), strings.ToLower(collapseColons(l))
		if strings.EqualFold(l, want) || c == lw || strings.EqualFold(LabelShort(l), want) ||
			strings.HasSuffix(strings.ToLower(l), ":"+lw) || strings.HasSuffix(c, ":"+lw) {
			out = append(out, l)
		}
	}
	return out
}

// resolveLabel rewrites --label to the stored label name. Two labels ending in the same short name is an
// error that lists them (exit 2); no match is left alone (the list is simply empty).
func (a *App) resolveLabel(o *Opts) error {
	want := o.str("label")
	if want == "" {
		return nil
	}
	switch c := matchLabel(want, a.labelUniverse(o)); len(c) {
	case 0:
	case 1:
		o.S["label"] = c[0]
	default:
		return die("label '%s' is ambiguous, it matches: %s\nuse the full name, for example -L %s", want, strings.Join(c, ", "), c[0])
	}
	return nil
}

// ---------- sweep ----------

type sweepItem struct {
	ref, reason, agent string
	ageSec             int
	ts                 float64
}

// sweepable lists the claims that should go: on a closed issue, or stale (past TTL or dead pid). A fresh
// claim on an open issue is never listed.
func (a *App) sweepable() []sweepItem { return a.sweepableFor(false, "") }

// sweepableFor is sweepable plus two switches for stopping work: live also lists FRESH claims on open issues (an
// agent that was killed cannot release its claim, and a claim with no pid looks alive until its lease runs out), and
// agent keeps only the claims that agent holds.
func (a *App) sweepableFor(live bool, agent string) []sweepItem {
	we := a.newWorkEnv()
	refs := make([]string, 0, len(we.claims))
	for r := range we.claims {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	var out []sweepItem
	for _, ref := range refs {
		if !a.inScope(shortOf(ref)) { // the project you are in; --all sweeps every repo
			continue
		}
		c := we.claims[ref]
		st := a.claimWork(we, ref)
		reason := ""
		switch {
		case st.Name == StDone:
			reason = "issue closed"
		case claimExpired(c, we.ctx):
			reason = "stale"
		case live:
			reason = "live claim, released on request"
		default:
			continue
		}
		if agent != "" && c.Agent != agent {
			continue
		}
		out = append(out, sweepItem{ref, reason, c.Agent, int(we.ctx.Now.Unix() - int64(c.TS)), c.TS})
	}
	return out
}

// cmdSweep lists claims that are stale or on closed issues; --release removes them. It never touches a fresh
// claim on an open issue, and a claim that changed since it was listed is left alone.
func (a *App) cmdSweep(o *Opts) error {
	v := a.resolveView(o, false)
	items := a.sweepableFor(o.B["live"], o.str("agent"))
	release := o.B["release"]
	released := map[string]bool{}
	if release {
		for _, it := range items {
			p, err := a.claimPath(it.ref)
			if err != nil {
				continue
			}
			if c := a.readClaim(p); c != nil && c.TS == it.ts && c.Agent == it.agent && a.mirror().remove(p) == nil {
				released[it.ref] = true
			}
		}
	}
	verb := "would release"
	if release {
		verb = "released"
	}
	switch v.format {
	case "json":
		l := []any{}
		for _, it := range items {
			l = append(l, Obj{{"ref", it.ref}, {"reason", it.reason}, {"agent", it.agent}, {"age_seconds", it.ageSec}, {"released", released[it.ref]}})
		}
		a.println(dumps(l, false))
	case "table":
		if len(items) == 0 {
			a.println("nothing to sweep: no stale claims, none on closed issues")
			return nil
		}
		cells := make([][]cell, len(items))
		for i, it := range items {
			act := verb
			if release && !released[it.ref] {
				act = "skipped (changed)"
			}
			cells[i] = []cell{{s: act, color: cDim}, {s: it.ref, color: cBold}, {s: it.reason, color: "status-stale"}, plain(it.agent), plain(humanDur(it.ageSec))}
		}
		a.Out.Write([]byte(renderTable([]col{{head: "ACTION"}, {head: "REF"}, {head: "WHY"}, {head: "AGENT", max: 24}, {head: "AGE"}}, cells, a.Width, a.styler())))
		if !release {
			fmt.Fprintf(a.Err, "%d claim(s) to release; run again with --release\n", len(items))
		}
	default:
		for _, it := range items {
			a.printf("%s\t%s\t%s\t%s\t%ds\n", verb, it.ref, it.reason, it.agent, it.ageSec)
		}
	}
	return nil
}
