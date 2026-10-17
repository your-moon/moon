package issues

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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

// push applies the outbox files in order, one attempt per op per run. Returns (pushed, failed, busy).
func (a *App) push(repoFilter, onlyRef string) (int, int, bool) {
	os.MkdirAll(a.Home, 0o755)
	f, err := os.OpenFile(filepath.Join(a.Home, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, 0, true
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // M_pushlock
		return 0, 0, true
	}
	pushed, failed := 0, 0
	badRefs, tried := map[string]bool{}, map[string]bool{}
	for {
		var op *Op
		all := a.loadOutbox()
		for i, o := range all {
			if badRefs[o.Ref] || tried[o.OpID] || msWaiting(all, i) {
				continue
			}
			if repoFilter != "" && shortOf(o.Ref) != repoFilter {
				continue
			}
			if onlyRef != "" && o.Ref != onlyRef {
				continue
			}
			op = o
			break
		}
		if op == nil {
			break
		}
		tried[op.OpID] = true
		if op.Op == "comment" && !op.Inflight {
			op.Inflight = true
			a.saveOp(op)
			op.Inflight = false // first attempt: no marker lookup needed
		}
		num, err := a.execute(op)
		if err != nil {
			op.Attempts++
			msg := trunc(err.Error(), 500)
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

func (a *App) cmdSimple(kind string) func(o *Opts) error {
	return func(o *Opts) error {
		args := map[string]any{}
		switch kind {
		case "comment":
			b, _, err := a.readBody(o)
			if err != nil {
				return err
			}
			if pyStrip(b) == "" {
				return die("comment needs --body or --body-file")
			}
			args["body"] = b
		case "close":
			args["reason"] = o.str("reason")
			if c := o.str("comment"); c != "" {
				args["comment"] = c
			}
		case "label":
			add, rm := o.L["add"], o.L["remove"]
			if len(add)+len(rm) == 0 {
				return die("label needs --add and/or --remove")
			}
			args["add"], args["remove"] = toAny(add), toAny(rm)
			if add == nil {
				args["add"] = []any{}
			}
			if rm == nil {
				args["remove"] = []any{}
			}
		case "milestone":
			t, err := a.msIssueTitle(o.Pos[0], o.Pos[1])
			if err != nil {
				return err
			}
			args["title"] = t
		case "edit":
			if o.Seen["title"] {
				args["title"] = o.str("title")
			}
			b, has, err := a.readBodyGuarded(o, "edit", false)
			if err != nil {
				return err
			}
			if has {
				args["body"] = b
			}
			if len(args) == 0 {
				return die("edit needs --title and/or --body")
			}
		}
		var opID, ref string
		if short, _, perr := a.parseRef(o.Pos[0]); perr == nil && a.localOnly(short) {
			if err := a.applyLocalOnly(kind, o.Pos[0], args); err != nil {
				return err
			}
			opID, ref = "local", o.Pos[0]
		} else {
			var err error
			opID, ref, err = a.enqueue(kind, o.Pos[0], args)
			if err != nil {
				return err
			}
		}
		extra := ""
		if kind == "close" {
			ag := o.str("agent")
			r := a.releaseClaim(ref, ag, !o.Seen["agent"])
			extra = "  claim: " + r
		}
		a.printf("op %s  ref %s%s\n", opID, ref, extra)
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
	var pushOnly []string // nil = everything queued; a scoped sync only sends its own repos' writes
	if a.scoped() {
		pushOnly = remote
	}
	rc := a.doSync(remote, o.B["quiet"], a.Out, pushOnly)
	if o.B["quiet"] { // the marker belongs to the background sync that this process is
		os.Remove(filepath.Join(a.Home, ".autosync"))
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
	ref, agent := o.Pos[0], o.str("agent")
	if _, _, err := a.parseRef(ref); err != nil {
		return err
	}
	os.MkdirAll(a.claimsDir(), 0o755)
	path, _ := a.claimPath(ref)
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
	mine := &Claim{agent, pid, a.Host, a.nowEpoch(), float64(ttl)}
	body := mine.marshal()
	taken := func(format string, args ...any) error {
		a.printf(format+"\n", args...)
		return &exitError{Code: 3}
	}
	for {
		if err := createExcl(path, body+"\n"); err == nil { // M_excl
			a.printf("claimed %s for %s\n", ref, agent)
			return nil
		} else if !os.IsExist(err) {
			return err
		}
		cur := a.readClaim(path)
		if cur == nil {
			continue // released between our open and read: retry
		}
		if cur.Agent == agent && !a.claimIsStale(cur) {
			atomicWrite(path, body+"\n", false)
			a.printf("renewed claim on %s for %s\n", ref, agent)
			return nil
		}
		if !a.claimIsStale(cur) {
			return taken("%s", a.heldMsg(ref, cur))
		}
		// stale: exactly one taker wins the rename, then races everyone else on O_EXCL
		gone := path + ".stale." + strings.ReplaceAll(uuid4(), "-", "")
		if err := os.Rename(path, gone); err != nil {
			return taken("TAKEN %s was just taken over by someone else", ref)
		}
		moved := a.readClaim(gone)
		os.Remove(gone)
		if moved != nil && !a.claimIsStale(moved) { // we renamed a fresh claim that replaced the stale one
			createExcl(path, moved.marshal()+"\n")
			return taken("%s", a.heldMsg(ref, moved))
		}
		if err := createExcl(path, body+"\n"); err != nil {
			return taken("TAKEN %s was just taken by someone else", ref)
		}
		a.printf("took over stale claim on %s (was %s); now held by %s\n", ref, a.holderText(cur), agent)
		return nil
	}
}

func (a *App) cmdRelease(o *Opts) error {
	force, agent := o.B["force"], o.str("agent")
	if !force && agent == "" {
		return die("release needs --agent NAME (you must be the holder) or --force")
	}
	if _, _, err := a.parseRef(o.Pos[0]); err != nil {
		return err
	}
	r := a.releaseClaim(o.Pos[0], agent, force)
	a.printf("%s: %s\n", o.Pos[0], r)
	if r == "released" || r == "none" {
		return nil
	}
	return &exitError{Code: 3}
}
