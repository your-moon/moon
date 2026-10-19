package issues

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *App) readBody(o *Opts) (string, bool, error) {
	if f := o.str("body-file"); f != "" {
		if f == "-" {
			b, err := io.ReadAll(a.Stdin)
			return string(b), true, err
		}
		s, err := readText(f)
		return s, true, err
	}
	if o.Seen["body"] {
		return o.str("body"), true, nil
	}
	return "", false, nil
}

// ---------- execute one op through gh ----------

var issueURLRe = regexp.MustCompile(`/issues/(\d+)`)

func sinceUTC(opTS string, now time.Time) string {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", opTS, time.Local)
	var u time.Time
	if err != nil {
		u = now.Add(-86400 * time.Second)
	} else {
		u = t.Add(-300 * time.Second)
	}
	return u.UTC().Format("2006-01-02T15:04:05Z")
}

// findByMarker: is there already an issue carrying <!-- local-id: lid -->? (create idempotency)
func (a *App) findByMarker(short, lid, opTS string) (int, error) {
	if n := a.localmapLookup(short, lid); n != 0 {
		return n, nil
	}
	marker := "<!-- local-id: " + lid + " -->"
	for _, p := range a.mirrorIssueFiles(short) {
		s, err := readText(p)
		if err == nil && strings.Contains(s, marker) {
			n, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(p), ".md"))
			return n, nil
		}
	}
	out, err := a.gh([]string{"api", fmt.Sprintf("repos/%s/issues?state=all&since=%s&per_page=100", a.repoFull(short), sinceUTC(opTS, a.Now())), "--paginate",
		"--jq", ".[] | {number: .number, body: .body}"}, "")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		if pyStrip(line) == "" {
			continue
		}
		var it struct {
			Number int    `json:"number"`
			Body   string `json:"body"`
		}
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			return 0, err
		}
		if strings.Contains(it.Body, marker) {
			return it.Number, nil
		}
	}
	out, err = a.gh([]string{"issue", "list", "-R", a.repoFull(short), "--search", lid + " in:body", "--state", "all", "--json", "number,body", "--limit", "5"}, "")
	if err != nil {
		return 0, err
	}
	var items []struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
	}
	if out = pyStrip(out); out != "" {
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			return 0, err
		}
	}
	for _, it := range items {
		if strings.Contains(it.Body, marker) {
			return it.Number, nil
		}
	}
	return 0, nil
}

// execute runs one op. Returns the new issue number for a create, else 0.
func (a *App) execute(op *Op) (int, error) {
	if isMsOp(op.Op) {
		return 0, a.executeMs(op)
	}
	if op.Op == "label_new" {
		return 0, a.executeLabelNew(op)
	}
	short, ident, err := a.parseRef(op.Ref)
	if err != nil {
		return 0, err
	}
	repo, args := a.repoFull(short), op.Args
	switch op.Op {
	case "new":
		n, err := a.findByMarker(short, argStr(args, "local_id"), op.TS)
		if err != nil {
			return 0, err
		}
		if n != 0 {
			return n, nil
		}
		ga := []string{"issue", "create", "-R", repo, "--title", argStr(args, "title"), "--body-file", "-"}
		for _, l := range argList(args, "labels") {
			ga = append(ga, "--label", l)
		}
		if m := argStr(args, "milestone"); m != "" {
			ga = append(ga, "--milestone", m)
		}
		out, err := a.gh(ga, argStr(args, "body"))
		if err != nil {
			return 0, err
		}
		m := issueURLRe.FindStringSubmatch(out)
		if m == nil {
			return 0, &GhError{Msg: fmt.Sprintf("create printed no issue url: %q", trunc(out, 200))}
		}
		n, _ = strconv.Atoi(m[1])
		return n, nil
	}
	if isLocalIdent(ident) {
		return 0, &GhError{Msg: fmt.Sprintf("unresolved local ref %s (its create has not landed)", op.Ref)}
	}
	n := ident
	switch op.Op {
	case "comment":
		marker := "<!-- op-id: " + op.OpID + " -->"
		if op.Inflight { // a previous attempt may have posted it before we could record that
			bodies, err := a.gh([]string{"api", fmt.Sprintf("repos/%s/issues/%s/comments", repo, n), "--paginate", "--jq", ".[].body"}, "")
			if err != nil {
				return 0, err
			}
			if strings.Contains(bodies, marker) {
				return 0, nil
			}
		}
		_, err := a.gh([]string{"issue", "comment", n, "-R", repo, "--body-file", "-"}, pyRstrip(argStr(args, "body"))+"\n\n"+marker)
		return 0, err
	case "close":
		reason := "completed"
		if argStr(args, "reason") == "not_planned" {
			reason = "not planned"
		}
		ga := []string{"issue", "close", n, "-R", repo, "--reason", reason}
		if c := argStr(args, "comment"); c != "" {
			ga = append(ga, "--comment", c)
		}
		_, err := a.gh(ga, "")
		return 0, err
	case "reopen":
		_, err := a.gh([]string{"issue", "reopen", n, "-R", repo}, "")
		return 0, err
	case "label":
		ga := []string{"issue", "edit", n, "-R", repo}
		if l := argList(args, "add"); len(l) > 0 {
			ga = append(ga, "--add-label", strings.Join(l, ","))
		}
		if l := argList(args, "remove"); len(l) > 0 {
			ga = append(ga, "--remove-label", strings.Join(l, ","))
		}
		_, err := a.gh(ga, "")
		return 0, err
	case "milestone":
		ga := []string{"issue", "edit", n, "-R", repo}
		if argStr(args, "title") == "none" {
			ga = append(ga, "--remove-milestone")
		} else {
			ga = append(ga, "--milestone", argStr(args, "title"))
		}
		_, err := a.gh(ga, "")
		return 0, err
	case "edit":
		ga := []string{"issue", "edit", n, "-R", repo}
		if t, ok := args["title"]; ok {
			ts, _ := t.(string)
			ga = append(ga, "--title", ts)
		}
		body, hasBody := args["body"]
		if hasBody {
			ga = append(ga, "--body-file", "-")
		}
		bs, _ := body.(string)
		_, err := a.gh(ga, bs)
		return 0, err
	}
	return 0, &GhError{Msg: "unknown op " + op.Op}
}

// ---------- push ----------

func (a *App) rewriteRefs(old, new string) {
	for _, o := range a.loadOutbox() {
		if o.Ref == old {
			o.Ref = new
			a.saveOp(o)
		}
	}
}

// pushWorkerLimit is how many queued writes push sends at once. Each write is about one `gh` round trip
// (3-4 s), so the wait of a sync was N round trips; independent writes now overlap (moon#17).
const pushWorkerLimit = 4

// pushWorkers is pushWorkerLimit; tests change it.
var pushWorkers = pushWorkerLimit

// orderKey is what a write must stay in order with: the writes of one issue (a `new` before its later edits,
// labels and close, which carry its local ref until the create lands), and the milestone writes of one repo.
func orderKey(o *Op) string {
	if isMsOp(o.Op) {
		return "ms:" + shortOf(o.Ref)
	}
	return o.Ref
}

// nextBatch is the writes that can go out now, at most limit: the first untried write of each order key,
// unless it must wait for a queued milestone. A write that waits holds back the later writes of its key, and
// so does one in flight (its key is in busy).
func nextBatch(all []*Op, badRefs, tried, busy map[string]bool, repoFilter, onlyRef string, limit int) []*Op {
	var batch []*Op
	if limit <= 0 {
		return nil
	}
	seen := map[string]bool{}
	for k := range busy {
		seen[k] = true
	}
	for i, o := range all {
		if badRefs[o.Ref] || tried[o.OpID] {
			continue
		}
		if o.Permanent { // never sent again; the writes behind it on the same issue wait too
			seen[orderKey(o)] = true
			continue
		}
		if repoFilter != "" && shortOf(o.Ref) != repoFilter {
			continue
		}
		if onlyRef != "" && o.Ref != onlyRef {
			continue
		}
		k := orderKey(o)
		if seen[k] {
			continue
		}
		seen[k] = true
		if msWaiting(all, i) || labelWaiting(all, i) {
			continue
		}
		batch = append(batch, o)
	}
	// longest chains first: the writes that must go one after another start earliest, so they do not end up
	// alone at the tail of the queue; ties keep queue order
	chain := map[string]int{}
	for _, o := range all {
		chain[orderKey(o)]++
	}
	sort.SliceStable(batch, func(i, j int) bool { return chain[orderKey(batch[i])] > chain[orderKey(batch[j])] })
	if len(batch) > limit {
		batch = batch[:limit]
	}
	return batch
}

type sent struct {
	num int
	err error
}

// push sends the outbox, one attempt per op per run, up to pushWorkers at a time (order kept per orderKey).
// Returns (pushed, failed, busy).
func (a *App) push(repoFilter, onlyRef string) (int, int, bool) {
	f, ok := a.tryPushLock()
	if !ok {
		return 0, 0, true
	}
	defer f.Close()
	pushed, failed := 0, 0
	badRefs, tried, busy := map[string]bool{}, map[string]bool{}, map[string]bool{}
	type done struct {
		op *Op
		sent
	}
	results := make(chan done)
	running := 0
	for {
		// keep the pool full: start every write that may go now
		for _, op := range nextBatch(a.loadOutbox(), badRefs, tried, busy, repoFilter, onlyRef, pushWorkers-running) {
			tried[op.OpID], busy[orderKey(op)] = true, true
			running++
			go func() {
				if op.Op == "comment" && !op.Inflight {
					op.Inflight = true
					a.saveOp(op)
					op.Inflight = false // first attempt: no marker lookup needed
				}
				num, err := a.execute(op)
				results <- done{op, sent{num, err}}
			}()
		}
		if running == 0 {
			break
		}
		// results are applied here, one at a time, so the outbox and localmap have a single writer
		r := <-results
		running--
		delete(busy, orderKey(r.op))
		op, num, err := r.op, r.num, r.err
		if err != nil {
			op.Attempts++
			msg := trunc(err.Error(), 500)
			if permanentError(msg) {
				op.Permanent = true
				msg = permanentPrefix + msg
			}
			op.LastError = &msg
			ge, isGh := err.(*GhError)
			op.Inflight = !(isGh && !ge.Timeout)
			a.saveOp(op)
			badRefs[op.Ref] = true
			failed++
			continue
		}
		if num != 0 {
			short, lid, _ := a.parseRef(op.Ref)
			real := fmt.Sprintf("%s#%d", short, num)
			lm, _ := os.OpenFile(a.localmap(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(lm, "%s\t%s\t%d\n", short, lid, num)
			lm.Close()
			a.rewriteRefs(op.Ref, real)
			os.Remove(op.File)
			a.rewriteRefs(op.Ref, real) // second scan: catches writers that raced the first one
		} else {
			os.Remove(op.File) // M_remove
		}
		pushed++
	}
	return pushed, failed, false
}

// ---------- command handlers ----------

func (a *App) cmdNew(o *Opts) error {
	if err := a.expandMsRef(o); err != nil {
		return err
	}
	body, _, err := a.readBodyGuarded(o, "new", true)
	if err != nil {
		return err
	}
	repo, err := a.scopeNeedsOne("new")
	if err != nil {
		return err
	}
	labels := o.L["label"]
	if labels == nil {
		labels = []string{}
	}
	if labels, err = a.checkLabels(repo, labels); err != nil {
		return err
	}
	if o.Seen["depends-on"] {
		nums, err := a.depNumbers(repo, o.L["depends-on"], 0)
		if err != nil {
			return err
		}
		body = withDepends(body, nums)
	}
	if a.localOnly(repo) {
		n, err := a.newLocalOnly(repo, o.str("title"), body, labels, o.str("milestone"))
		if err != nil {
			return err
		}
		a.printf("ref %s#%d  (#%d)\n", repo, n, n)
		return nil
	}
	seq, err := a.nextLocalSeq(repo)
	if err != nil {
		return err
	}
	lid := fmt.Sprintf("L-%d", seq)
	body = pyRstrip(body) + "\n\n<!-- local-id: " + lid + " -->"
	args := map[string]any{"title": o.str("title"), "body": body, "labels": toAny(labels), "local_id": lid}
	if o.Seen["milestone"] {
		args["milestone"] = o.str("milestone")
	} else {
		args["milestone"] = nil
	}
	opID, ref, err := a.enqueue("new", repo+"#"+lid, args)
	if err != nil {
		return err
	}
	if !o.B["now"] {
		a.printf("op %s  ref %s\n", opID, ref)
		return nil
	}
	_, _, busy := a.push("", ref)
	n := a.localmapLookup(repo, lid)
	if n == 0 {
		why := "gh failed"
		if busy {
			why = "another push is running"
		}
		le := "None"
		for _, op := range a.loadOutbox() {
			if op.OpID == opID && op.LastError != nil {
				le = *op.LastError
			}
		}
		return die1("create not pushed (%s); it stays queued as %s: %s", why, ref, le)
	}
	a.pull(repo, false) // a failed refresh is not an error here
	a.printf("op %s  ref %s#%d  (#%d)\n", opID, repo, n, n)
	return nil
}

func toAny(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}

// labels queues label changes on ref: add (checked against the repo's labels) and remove.
func (a *App) labels(ref string, add, rm []string) (opResult, error) {
	if len(add)+len(rm) == 0 {
		return opResult{}, die("label needs --add and/or --remove")
	}
	short, _, err := a.parseRef(ref)
	if err != nil {
		return opResult{}, err
	}
	if add, err = a.checkLabels(short, add); err != nil {
		return opResult{}, err
	}
	return a.queueOp("label", ref, map[string]any{"add": toAny(add), "remove": toAny(rm)})
}

// opResult is what a queued write reports: the op id, the ref it landed on, and (close only) what happened to the agent's claim.
type opResult struct {
	OpID, Ref, Claim string
}

func (r opResult) line() string {
	extra := ""
	if r.Claim != "" {
		extra = "  claim: " + r.Claim
	}
	return fmt.Sprintf("op %s  ref %s%s\n", r.OpID, r.Ref, extra)
}

// queueOp applies one write op to ref: straight to the file for a local-only repo, else onto the outbox.
func (a *App) queueOp(kind, ref string, args map[string]any) (opResult, error) {
	if short, _, perr := a.parseRef(ref); perr == nil && a.localOnly(short) {
		if err := a.applyLocalOnly(kind, ref, args); err != nil {
			return opResult{}, err
		}
		return opResult{OpID: "local", Ref: ref}, nil
	}
	opID, r, err := a.enqueue(kind, ref, args)
	if err != nil {
		return opResult{}, err
	}
	return opResult{OpID: opID, Ref: r}, nil
}

// closeIssue queues a close with reason and optional comment, then drops agent's claim on the issue
// (any claim when the caller named no agent: noAgent), as `moon issues close` does.
func (a *App) closeIssue(ref, reason, comment, agent string, noAgent bool) (opResult, error) {
	args := map[string]any{"reason": reason}
	if comment != "" {
		args["comment"] = comment
	}
	r, err := a.queueOp("close", ref, args)
	if err != nil {
		return r, err
	}
	r.Claim = a.releaseClaim(r.Ref, agent, noAgent)
	return r, nil
}

// reopen queues reopening ref.
func (a *App) reopen(ref string) (opResult, error) {
	return a.queueOp("reopen", ref, map[string]any{})
}

// comment queues a comment on ref; a blank body is refused.
func (a *App) comment(ref, body string) (opResult, error) {
	if pyStrip(body) == "" {
		return opResult{}, die("comment needs --body or --body-file")
	}
	return a.queueOp("comment", ref, map[string]any{"body": body})
}

// setMilestone queues a milestone change on ref; title "none" clears it.
func (a *App) setMilestone(ref, title string) (opResult, error) {
	t, err := a.msIssueTitle(ref, title)
	if err != nil {
		return opResult{}, err
	}
	return a.queueOp("milestone", ref, map[string]any{"title": t})
}

func (a *App) cmdSimple(kind string) func(o *Opts) error {
	return func(o *Opts) error {
		args := map[string]any{}
		switch kind {
		case "comment":
			b, _, err := a.readBody(o)
			if err != nil {
				return err
			}
			r, err := a.comment(o.Pos[0], b)
			if err != nil {
				return err
			}
			a.printf("%s", r.line())
			return nil
		case "close":
			r, err := a.closeIssue(o.Pos[0], o.str("reason"), o.str("comment"), o.str("agent"), !o.Seen["agent"])
			if err != nil {
				return err
			}
			a.printf("%s", r.line())
			return nil
		case "reopen":
			r, err := a.reopen(o.Pos[0])
			if err != nil {
				return err
			}
			a.printf("%s", r.line())
			return nil
		case "label":
			r, err := a.labels(o.Pos[0], o.L["add"], o.L["remove"])
			if err != nil {
				return err
			}
			a.printf("%s", r.line())
			return nil
		case "milestone":
			r, err := a.setMilestone(o.Pos[0], o.Pos[1])
			if err != nil {
				return err
			}
			a.printf("%s", r.line())
			return nil
		case "edit":
			if o.Seen["title"] {
				args["title"] = o.str("title")
			}
			b, has, err := a.readBodyGuarded(o, "edit", false)
			if err != nil {
				return err
			}
			if o.Seen["depends-on"] {
				if b, has, err = a.editDepends(o.Pos[0], b, has, o.L["depends-on"]); err != nil {
					return err
				}
			}
			if has {
				args["body"] = b
			}
			if len(args) == 0 {
				return die("edit needs --title, --body and/or --depends-on")
			}
		}
		r, err := a.queueOp(kind, o.Pos[0], args)
		if err != nil {
			return err
		}
		a.printf("%s", r.line())
		return nil
	}
}

// pushRepos pushes the outbox of the given repos (nil = every repo), summing the results.
func (a *App) pushRepos(shorts []string, onlyRef string) (int, int, bool) {
	if shorts == nil || len(shorts) == len(a.Repos) {
		return a.push("", onlyRef)
	}
	p, f, busy := 0, 0, false
	for _, s := range shorts {
		pp, ff, bb := a.push(s, onlyRef)
		p, f, busy = p+pp, f+ff, busy || bb
	}
	return p, f, busy
}

func (a *App) cmdPush(o *Opts) error {
	remote, local := a.splitLocal(a.shortsFor(o))
	if len(remote) == 0 {
		a.noteLocalOnly(o, local, 0)
		return nil
	}
	p, f, busy := a.pushRepos(remote, "")
	s := ""
	if busy {
		s = " (another push is running)"
	}
	a.printf("PUSH pushed=%d failed=%d%s\n", p, f, s)
	if f > 0 {
		return &exitError{Code: 1}
	}
	return nil
}

func (a *App) cmdPull(o *Opts) error {
	rc := 0
	parts := []string{}
	remote, local := a.splitLocal(a.shortsFor(o))
	a.noteLocalOnly(o, local, len(remote))
	if len(remote) == 0 {
		return nil
	}
	for _, short := range remote {
		n, err := a.pull(short, o.B["full"])
		if err != nil {
			parts = append(parts, short+"=ERR")
			fmt.Fprintf(a.Err, "pull %s failed: %s\n", short, err)
			rc = 1
		} else {
			parts = append(parts, fmt.Sprintf("%s=%d", short, n))
		}
	}
	a.printf("PULL %s\n", strings.Join(parts, " "))
	if rc != 0 {
		return &exitError{Code: rc}
	}
	return nil
}

func (a *App) cmdPrune(o *Opts) error {
	parts := []string{}
	remote, _ := a.splitLocal(a.shortsFor(o)) // local-only issues are canonical: never pruned
	for _, short := range remote {
		a.withLock(".pull.lock", func() error {
			parts = append(parts, fmt.Sprintf("%s=%d", short, a.pruneFiles(short)))
			return nil
		})
	}
	a.printf("PRUNE removed %s\n", strings.Join(parts, " "))
	return nil
}

// doSync is push + incremental pull. Returns the exit code.
func (a *App) doSync(shorts []string, quiet bool, out io.Writer, pushOnly []string) int {
	t0 := time.Now()
	p, f, busy := a.pushRepos(pushOnly, "")
	rc := 0
	if f > 0 {
		rc = 1
	}
	parts := []string{}
	for _, short := range shorts {
		n, err := a.pull(short, false)
		if err != nil {
			parts = append(parts, short+"=ERR")
			fmt.Fprintf(a.Err, "pull %s failed: %s\n", short, err)
			rc = 1
		} else {
			parts = append(parts, fmt.Sprintf("%s=%d", short, n))
		}
	}
	skip := ""
	if busy {
		skip = " (push skipped: another push is running)"
	}
	line := fmt.Sprintf("SYNC pushed=%d failed=%d pulled %s in %.1fs%s", p, f, strings.Join(parts, " "), time.Since(t0).Seconds(), skip)
	if quiet {
		line = a.nowISO() + " " + line
	}
	fmt.Fprintln(out, line)
	return rc
}

func (a *App) cmdSync(o *Opts) error {
	remote, local := a.splitLocal(a.shortsFor(o))
	if !o.B["quiet"] {
		a.noteLocalOnly(o, local, len(remote))
	}
	if len(remote) == 0 {
		return nil
	}
	// only the writes of the repos being synced: a scoped sync leaves the others alone, and a local-only repo's
	// writes are never sent (pushRepos sends everything queued when remote is every repo)
	rc := a.doSync(remote, o.B["quiet"], a.Out, remote)
	if o.B["quiet"] { // the marker belongs to the background sync that this process is
		a.releaseSyncMarker(remote)
	}
	if rc != 0 {
		return &exitError{Code: rc}
	}
	return nil
}

// ---------- claims ----------

var ttlRe = regexp.MustCompile(`^(\d+)([smhd]?)$`)

func parseTTL(t string) (int, error) {
	m := ttlRe.FindStringSubmatch(t)
	if m == nil {
		return 0, die("bad --ttl '%s' (want 90s, 120m, 2h)", t)
	}
	n, _ := strconv.Atoi(m[1])
	mult := map[string]int{"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}[m[2]]
	return n * mult, nil
}

func (a *App) cmdClaim(o *Opts) error {
	if _, _, err := a.parseRef(o.Pos[0]); err != nil { // a bad ref is reported before a bad --ttl
		return err
	}
	ttlStr := o.str("ttl")
	if ttlStr == "" {
		ttlStr = "120m"
		if a.Cfg != nil && a.Cfg.ClaimTTL != "" {
			ttlStr = a.Cfg.ClaimTTL
		}
	}
	ttl, err := parseTTL(ttlStr)
	if err != nil {
		return err
	}
	pid, _ := strconv.Atoi(o.str("pid"))
	msg, err := a.claim(o.Pos[0], o.str("agent"), ttl, pid)
	if msg != "" {
		a.printf("%s\n", msg)
	}
	if errors.Is(err, ErrHeld) {
		return &exitError{Code: 3}
	}
	return err
}

// claim takes (or renews, or takes over a stale) claim on ref for agent and returns the one line
// `moon issues claim` prints. A claim held by someone else comes back as ErrHeld together with the
// line saying who holds it; it is never forced. ttl is in seconds.
func (a *App) claim(ref, agent string, ttl, pid int) (string, error) {
	if _, _, err := a.parseRef(ref); err != nil {
		return "", err
	}
	os.MkdirAll(a.claimsDir(), 0o755)
	path, _ := a.claimPath(ref)
	mine := &Claim{agent, pid, a.Host, a.nowEpoch(), float64(ttl)}
	body := mine.marshal()
	for {
		if err := createExcl(path, body+"\n"); err == nil { // M_excl
			return fmt.Sprintf("claimed %s for %s", ref, agent), nil
		} else if !os.IsExist(err) {
			return "", err
		}
		cur := a.readClaim(path)
		if cur == nil {
			continue // released between our open and read: retry
		}
		if cur.Agent == agent && !a.claimIsStale(cur) {
			atomicWrite(path, body+"\n", false)
			return fmt.Sprintf("renewed claim on %s for %s", ref, agent), nil
		}
		if !a.claimIsStale(cur) {
			return a.heldMsg(ref, cur), ErrHeld
		}
		// stale: exactly one taker wins the rename, then races everyone else on O_EXCL
		gone := path + ".stale." + strings.ReplaceAll(uuid4(), "-", "")
		if err := os.Rename(path, gone); err != nil {
			return fmt.Sprintf("TAKEN %s was just taken over by someone else", ref), ErrHeld
		}
		moved := a.readClaim(gone)
		os.Remove(gone)
		if moved != nil && !a.claimIsStale(moved) { // we renamed a fresh claim that replaced the stale one
			createExcl(path, moved.marshal()+"\n")
			return a.heldMsg(ref, moved), ErrHeld
		}
		if err := createExcl(path, body+"\n"); err != nil {
			return fmt.Sprintf("TAKEN %s was just taken by someone else", ref), ErrHeld
		}
		return fmt.Sprintf("took over stale claim on %s (was %s); now held by %s", ref, a.holderText(cur), agent), nil
	}
}

func (a *App) cmdRelease(o *Opts) error {
	force, agent := o.B["force"], o.str("agent")
	if !force && agent == "" {
		return die("release needs --agent NAME (you must be the holder) or --force")
	}
	msg, err := a.release(o.Pos[0], agent, force)
	if msg != "" {
		a.printf("%s\n", msg)
	}
	if errors.Is(err, ErrHeld) {
		return &exitError{Code: 3}
	}
	return err
}

// release drops agent's claim on ref (any claim with force) and returns the line `moon issues
// release` prints. Someone else's claim comes back as ErrHeld together with that line.
func (a *App) release(ref, agent string, force bool) (string, error) {
	if _, _, err := a.parseRef(ref); err != nil {
		return "", err
	}
	r := a.releaseClaim(ref, agent, force)
	msg := ref + ": " + r
	if r == "released" || r == "none" {
		return msg, nil
	}
	return msg, ErrHeld
}
