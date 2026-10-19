package issues

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Milestone writes, local first. For a GitHub repo `milestones new|edit|close|reopen` queue an op in
// the outbox (ms_new, ms_edit, ms_close, ms_reopen) and `sync` sends it; a milestone created here is
// M-<n> until then. For a local-only repo the same commands write <alias>/milestones.tsv directly.

// ---------- M-<n> counter ----------

func (a *App) mseqPath(short string) string { return filepath.Join(a.Home, short, "local-mseq") }

// nextMilestoneSeq hands out the next M-<n> (or, for a local-only repo, the next milestone number).
// Same design as nextLocalSeq: an exclusive flock on a sidecar, a counter that only goes up.
func (a *App) nextMilestoneSeq(short string) (int, error) {
	dir := filepath.Join(a.Home, short)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "local-mseq.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil { // M_msseqlock
		return 0, err
	}
	last := 0
	if s, err := readText(a.mseqPath(short)); err == nil {
		if n, perr := strconv.Atoi(pyStrip(s)); perr == nil && n >= 0 {
			last = n
		} else {
			last = a.scanMaxMsSeq(short)
		}
	} else if isNotExist(err) {
		last = a.scanMaxMsSeq(short)
	} else {
		return 0, err
	}
	n := last + 1
	if err := atomicWrite(a.mseqPath(short), strconv.Itoa(n)+"\n", true); err != nil {
		return 0, err
	}
	return n, nil
}

func msSeqOf(id string) int {
	if m := msRefRe.FindStringSubmatch(id); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// scanMaxMsSeq rebuilds a lost counter from the outbox, localmap and milestones.tsv.
func (a *App) scanMaxMsSeq(short string) int {
	max := 0
	note := func(n int) {
		if n > max {
			max = n
		}
	}
	for _, o := range a.loadOutbox() {
		if o.Op == "ms_new" && shortOf(o.Ref) == short {
			note(msSeqOf(argStr(o.Args, "local_id")))
		}
	}
	if s, err := readText(a.localmap()); err == nil {
		for _, line := range strings.Split(s, "\n") {
			if p := strings.Split(line, "\t"); len(p) == 3 && p[0] == short {
				note(msSeqOf(p[1]))
			}
		}
	}
	for _, r := range a.readMsFile(short) {
		note(msSeqOf(r.LocalID))
		if a.localOnly(short) {
			note(r.Number)
		}
	}
	return max
}

// ---------- arguments ----------

// msDueArg validates -d. none is only allowed on edit. Returns "" when the flag was not given.
func msDueArg(o *Opts, allowNone bool) (string, error) {
	if !o.Seen["due"] {
		return "", nil
	}
	d := pyStrip(o.str("due"))
	if allowNone && strings.EqualFold(d, "none") {
		return "none", nil
	}
	if _, err := time.Parse("2006-01-02", d); err != nil || len(d) != 10 {
		hint := ""
		if allowNone {
			hint = ", or none to clear it"
		}
		return "", die("bad due date '%s': want a real date as YYYY-MM-DD, for example 2026-12-31%s", d, hint)
	}
	return d, nil
}

func (a *App) readDesc(o *Opts) (string, bool, error) {
	if o.Seen["description"] && o.str("description-file") != "" {
		return "", false, die("use either --description or --description-file, not both")
	}
	if f := o.str("description-file"); f != "" {
		if f == "-" {
			b, err := io.ReadAll(a.Stdin)
			return pyRstrip(string(b)), true, err
		}
		s, err := readText(f)
		if err != nil {
			return "", false, die("cannot read --description-file %s: %v", f, err)
		}
		return pyRstrip(s), true, nil
	}
	if o.Seen["description"] {
		return o.str("description"), true, nil
	}
	return "", false, nil
}

func dupError(short, title string, d *msRec) error {
	msg := fmt.Sprintf("milestone '%s' already exists in %s", title, short)
	if d.Title != title {
		msg = fmt.Sprintf("milestone '%s' already exists in %s as '%s' (titles are unique, ignoring case)", title, short, d.Title)
	}
	switch {
	case d.pending && d.LocalID != "":
		msg += fmt.Sprintf(" (queued as %s)", d.LocalID)
	case d.Number == 0 && d.LocalID == "":
		msg += "; it is only named by issues so far: set its due date or description with: moon issues milestones edit '" + d.Title + "' -d DATE"
	}
	return die("%s", msg)
}

// enqueueMs queues one milestone op. Milestone refs are not issue refs, so this skips parseRef.
func (a *App) enqueueMs(op, ref string, args map[string]any) (string, error) {
	t := a.Now()
	stem := fmt.Sprintf("%s.%06d-%d-%s", t.Format("20060102T150405"), t.UnixMicro()%1000000, os.Getpid(), uuid4())
	o := &Op{OpID: stem, TS: a.nowISO(), Op: op, Ref: ref, Args: args, File: filepath.Join(a.outbox(), stem+".json")}
	return stem, a.saveOp(o)
}

// ---------- commands ----------

func (a *App) cmdMsNew(o *Opts) error {
	title := pyStrip(o.Pos[0])
	if title == "" || strings.ContainsAny(title, "\t\r\n") {
		return die("a milestone title is one non-empty line, got %q", o.Pos[0])
	}
	due, err := msDueArg(o, false)
	if err != nil {
		return err
	}
	desc, _, err := a.readDesc(o)
	if err != nil {
		return err
	}
	short, err := a.msRepo(o)
	if err != nil {
		return err
	}
	return a.withLock(".ms.lock", func() error {
		if d := msDup(a.msRecs(short), title, ""); d != nil {
			return dupError(short, title, d)
		}
		n, err := a.nextMilestoneSeq(short)
		if err != nil {
			return err
		}
		if a.localOnly(short) {
			err := a.withLock(".pull.lock", func() error {
				recs := append(a.readMsFile(short), msRec{Number: n, Title: title, State: "open", Due: due, Desc: desc, Updated: a.stamp()})
				return a.writeMsFile(short, recs)
			})
			if err == nil {
				a.printf("milestone %s \"%s\"  (#%d)\n", short, title, n)
			}
			return err
		}
		lid := fmt.Sprintf("M-%d", n)
		opID, err := a.enqueueMs("ms_new", short+"#"+lid, map[string]any{"title": title, "description": desc, "due": due, "local_id": lid})
		if err != nil {
			return err
		}
		a.printf("milestone %s#%s \"%s\"  op %s\n", short, lid, title, opID)
		return nil
	})
}

func (a *App) cmdMsEdit(o *Opts) error {
	short, rec, err := a.msFindRepo(o, o.Pos[0])
	if err != nil {
		return err
	}
	args := map[string]any{}
	if o.Seen["title"] {
		t := pyStrip(o.str("title"))
		if t == "" || strings.ContainsAny(t, "\t\r\n") {
			return die("a milestone title is one non-empty line, got %q", o.str("title"))
		}
		if d := msDup(a.msRecs(short), t, rec.Title); d != nil {
			return dupError(short, t, d)
		}
		if t != rec.Title {
			args["new_title"] = t
		}
	}
	due, err := msDueArg(o, true)
	if err != nil {
		return err
	}
	if o.Seen["due"] {
		args["due"] = due
	}
	desc, has, err := a.readDesc(o)
	if err != nil {
		return err
	}
	if has {
		args["description"] = desc
	}
	if !o.Seen["title"] && !o.Seen["due"] && !has {
		return die("edit needs --title, -d/--due (a date or none) and/or -D/--description")
	}
	if len(args) == 0 {
		a.printf("milestone %s \"%s\": title unchanged, nothing to do\n", short, rec.Title)
		return nil
	}
	return a.msChange(short, rec, "ms_edit", args)
}

func (a *App) cmdMsState(op string) func(*Opts) error {
	return func(o *Opts) error {
		short, rec, err := a.msFindRepo(o, o.Pos[0])
		if err != nil {
			return err
		}
		want := map[string]string{"ms_close": "closed", "ms_reopen": "open"}[op]
		if rec.State == want {
			a.printf("milestone %s \"%s\" is already %s\n", short, rec.Title, want)
			return nil
		}
		return a.msChange(short, rec, op, map[string]any{})
	}
}

// msChange applies one edit/close/reopen: straight to the file for a local-only repo, else queued.
func (a *App) msChange(short string, rec msRec, op string, args map[string]any) error {
	if a.localOnly(short) {
		if err := a.applyMsLocalOnly(short, rec.Title, op, args); err != nil {
			return err
		}
		a.printf("milestone %s \"%s\" %s\n", short, rec.Title, strings.TrimPrefix(op, "ms_"))
		return nil
	}
	args["title"] = rec.Title
	ref := short + "#ms"
	if rec.Number == 0 && rec.LocalID != "" {
		args["local_id"], ref = rec.LocalID, short+"#"+rec.LocalID
	}
	opID, err := a.enqueueMs(op, ref, args)
	if err != nil {
		return err
	}
	a.printf("milestone %s \"%s\" %s  op %s\n", short, rec.Title, strings.TrimPrefix(op, "ms_"), opID)
	return nil
}

// applyMsLocalOnly performs one milestone write on the file of a local-only repo. A title that only
// issues name gets a row (and a number) the first time it is changed.
func (a *App) applyMsLocalOnly(short, title, op string, args map[string]any) error {
	return a.withLock(".ms.lock", func() error {
		return a.withLock(".pull.lock", func() error {
			recs := a.readMsFile(short)
			idx := -1
			for i, r := range recs {
				if r.Title == title {
					idx = i
				}
			}
			if idx < 0 {
				st := "open"
				if _, open, _ := a.msCounts(short); open[title] == 0 {
					st = "closed"
				}
				n, err := a.nextMilestoneSeq(short)
				if err != nil {
					return err
				}
				recs = append(recs, msRec{Number: n, Title: title, State: st})
				idx = len(recs) - 1
			}
			applyMsChange(&recs[idx], op, args)
			recs[idx].Updated = a.stamp()
			if t, ok := args["new_title"].(string); ok {
				if err := a.renameMsInMirror(short, title, t); err != nil {
					return err
				}
			}
			return a.writeMsFile(short, recs)
		})
	})
}

// renameMsInMirror rewrites the milestone title in the issue files and index of alias. The caller holds .pull.lock.
func (a *App) renameMsInMirror(short, old, new string) error {
	for _, p := range a.mirrorIssueFiles(short) {
		h := readHeader(p)
		if h == nil || hStr(h, "milestone") != old {
			continue
		}
		node, comments, err := loadLocalIssue(p)
		if err != nil {
			continue
		}
		node.Milestone = &struct {
			Title string `json:"title"`
		}{new}
		if err := atomicWrite(p, renderIssue(node)+comments, false); err != nil {
			return err
		}
	}
	return a.rebuildIndex(short, nil, nil)
}

// msIssueTitle resolves the <title> of `milestone <ref> <title>`: M-<n> becomes the milestone's title.
func (a *App) msIssueTitle(ref, typed string) (string, error) {
	short, _, err := a.parseRef(ref)
	if err != nil || !msRefRe.MatchString(typed) {
		return typed, nil
	}
	t, _, err := a.msTitleOfRef(short, typed)
	return t, err
}

// ---------- sync: what the push loop needs ----------

// msWaiting reports whether ops[i] mentions a milestone title that a milestone op still in the outbox
// has to create or rename into first. Such an op waits, so the milestone always exists on GitHub
// before an issue is pointed at it. A queued ms_new counts wherever it sits in the outbox; a rename
// only counts when it was queued earlier (so two opposite renames cannot wait on each other).
func msWaiting(ops []*Op, i int) bool {
	o := ops[i]
	title := ""
	if k := kindOrNil(o.Op); k != nil && k.waitTitle != nil {
		title = k.waitTitle(o)
	}
	if title == "" {
		return false
	}
	short := shortOf(o.Ref)
	for j, p := range ops {
		if j == i || shortOf(p.Ref) != short {
			continue
		}
		switch {
		case p.Op == "ms_new" && strings.EqualFold(argStr(p.Args, "title"), title):
			return true
		case p.Op == "ms_edit" && j < i && strings.EqualFold(argStr(p.Args, "new_title"), title):
			return true
		}
	}
	return false
}

func (a *App) appendLocalmap(short, id string, n int) error {
	lm, err := os.OpenFile(a.localmap(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer lm.Close()
	_, err = fmt.Fprintf(lm, "%s\t%s\t%d\n", short, id, n)
	return err
}

// msOpRepo is the alias and GitHub repo a milestone op is about.
func (a *App) msOpRepo(op *Op) (short, repo string, err error) {
	short = shortOf(op.Ref)
	repo = a.repoFull(short)
	if repo == "" {
		return "", "", &GhError{Msg: fmt.Sprintf("no GitHub repo behind alias %s", short)}
	}
	return short, repo, nil
}

// executeMsNew sends a milestone create.
func (a *App) executeMsNew(op *Op) error {
	short, repo, err := a.msOpRepo(op)
	if err != nil {
		return err
	}
	return a.execMsNew(short, repo, op)
}

// executeMsChange sends a close, reopen or edit of a milestone to GitHub (change fills the request from the
// op's args) and then records the result in the mirror.
func (a *App) executeMsChange(op *Op, change func(ch *MilestoneChange, args map[string]any)) error {
	short, repo, err := a.msOpRepo(op)
	if err != nil {
		return err
	}
	args := op.Args
	num, err := a.msNumber(short, repo, args)
	if err != nil {
		return err
	}
	ch := MilestoneChange{}
	change(&ch, args)
	if err := a.GH.UpdateMilestone(repo, num, ch); err != nil {
		return err
	}
	return a.msMirror(short, num, argStr(args, "title"), "", func(r *msRec) { applyMsChange(r, op.Op, args) }, argStr(args, "new_title"))
}

// msNumber finds the GitHub number of the milestone an edit, close or reopen is about.
func (a *App) msNumber(short, repo string, args map[string]any) (int, error) {
	if lid := argStr(args, "local_id"); lid != "" {
		if n := a.localmapLookup(short, lid); n != 0 {
			return n, nil
		}
		return 0, &GhError{Msg: fmt.Sprintf("milestone %s is not on GitHub yet (its create has not landed)", lid)}
	}
	title := argStr(args, "title")
	for _, r := range a.readMsFile(short) {
		if r.Title == title && r.Number > 0 {
			return r.Number, nil
		}
	}
	list, err := a.GH.ListMilestones(repo)
	if err != nil {
		return 0, err
	}
	for _, r := range list {
		if r.Title == title {
			return r.Number, nil
		}
	}
	return 0, &GhError{Msg: fmt.Sprintf("no milestone '%s' on %s", title, repo)}
}

// execMsNew creates the milestone on GitHub, or adopts the one that is already there. Safe to run
// again after a crash anywhere in between: it looks the title up before it creates, and a milestone
// whose title only differs in case (which a lookup by exact title cannot tell apart) is created with a
// hidden op marker in its description, so a retry finds its own.
func (a *App) execMsNew(short, repo string, op *Op) error {
	args := op.Args
	lid, title, due, desc := argStr(args, "local_id"), argStr(args, "title"), argStr(args, "due"), argStr(args, "description")
	if lid != "" && a.localmapLookup(short, lid) != 0 {
		return nil // already created and recorded
	}
	marker := "<!-- moon-op: " + op.OpID + " -->"
	find := func() (*RemoteMilestone, bool, error) {
		list, err := a.GH.ListMilestones(repo)
		if err != nil {
			return nil, false, err
		}
		ci := false
		for i := range list {
			if strings.Contains(list[i].Description, marker) {
				return &list[i], false, nil
			}
		}
		for i := range list {
			if list[i].Title == title {
				return &list[i], false, nil
			}
			if strings.EqualFold(list[i].Title, title) {
				ci = true
			}
		}
		return nil, ci, nil
	}
	found, ambiguous, err := find()
	if err != nil {
		return err
	}
	if found == nil {
		d := desc
		if ambiguous {
			d = pyRstrip(desc + "\n\n" + marker)
		}
		created, cerr := a.GH.CreateMilestone(repo, RemoteMilestone{Title: title, DueOn: due, Description: d})
		if cerr != nil {
			// somebody (or an earlier attempt) created it meanwhile: adopt rather than fail
			f2, _, ferr := find()
			if ferr != nil || f2 == nil {
				return cerr
			}
			found = f2
		} else {
			found = &created
			found.State = "open"
		}
	}
	// adopted: fill what the remote lacks, never overwrite what it has
	ch := MilestoneChange{}
	if found.DueOn == "" && due != "" {
		ch.Due = &due
		found.DueOn = due
	}
	if clean := pyStrip(opMarkerRe.ReplaceAllString(found.Description, "")); clean == "" && desc != "" {
		ch.Description = &desc
		found.Description = desc
	}
	if ch.Due != nil || ch.Description != nil {
		if err := a.GH.UpdateMilestone(repo, found.Number, ch); err != nil {
			return err
		}
	}
	num := found.Number
	if lid != "" {
		if err := a.appendLocalmap(short, lid, num); err != nil {
			return err
		}
	}
	rec := msRec{Number: num, LocalID: lid, Title: found.Title, State: found.State, Due: found.DueOn,
		Desc: pyStrip(opMarkerRe.ReplaceAllString(found.Description, "")), Updated: a.stamp()}
	return a.msMirror(short, num, title, lid, func(r *msRec) { *r = rec }, "")
}

// msMirror records the new state of one milestone in milestones.tsv right after GitHub accepted it, so a
// read after `push` is right before the next pull. The row is found by number, else by its old title;
// edit changes it, and a row that is not there yet is added only when addLid is non-empty (a create).
func (a *App) msMirror(short string, num int, title, addLid string, edit func(*msRec), renamedTo string) error {
	return a.withLock(".pull.lock", func() error {
		recs := a.readMsFile(short)
		idx := -1
		for i, r := range recs {
			if (num > 0 && r.Number == num) || (r.Number == 0 && r.Title == title) {
				idx = i
			}
		}
		if renamedTo != "" && renamedTo != title { // GitHub renamed it on its issues too
			if err := a.renameMsInMirror(short, title, renamedTo); err != nil {
				return err
			}
		}
		if idx < 0 {
			if addLid == "" {
				return nil
			}
			recs = append(recs, msRec{})
			idx = len(recs) - 1
		}
		recs[idx].legacy = false
		edit(&recs[idx])
		recs[idx].Number = num
		if recs[idx].LocalID == "" {
			recs[idx].LocalID = a.msLocalIDs(short)[num]
		}
		recs[idx].Updated = a.stamp()
		return a.writeMsFile(short, recs)
	})
}
