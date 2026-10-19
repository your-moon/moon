package issues

import (
	"encoding/json"
	"strings"
	"testing"
)

func opIDOf(t *testing.T, e *env, op string) string {
	t.Helper()
	for _, o := range e.app().loadOutbox() {
		if o.Op == op {
			return o.OpID
		}
	}
	t.Fatalf("no %s op queued", op)
	return ""
}

// The incident: a queued create with a bad label retried forever and could only be repaired by hand-editing JSON.
func TestOutboxEditFixesAStuckCreate(t *testing.T) {
	e := newEnv(t)
	g := e.useMem() // GitHub has bug and area::x, but not the label `bad`: a create with it is rejected
	g.addLabels("example/app", "bug", "area::x")
	e.run("new", "-R", "app", "--title", "t", "--body", "text", "-L", "bad")
	if _, _, code := e.run("push"); code != 1 {
		t.Fatalf("push of a rejected create must fail, code %d", code)
	}
	id := opIDOf(t, e, "new")
	out, _, _ := e.run("outbox", "ls")
	want := id + "\tnew\tapp#L-1\t1\tnew: t [bad]\tcould not add label: 'bad' not found\n"
	eq(t, "outbox ls", out, want)

	out, errs, code := e.run("outbox", "edit", id[len(id)-8:], "--label", "bug", "--label", "area::x")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	contains0(t, "edit output", out, "edited "+id+"  new app#L-1  new: t [bug,area::x]")
	if o := e.app().loadOutbox()[0]; o.LastError != nil || o.Attempts != 1 {
		t.Fatalf("the old error must be cleared, attempts kept: %+v", o)
	}
	if _, errs, code := e.run("push"); code != 0 {
		t.Fatalf("push after the fix: %d %s", code, errs)
	}
	if len(g.created) != 1 {
		t.Fatalf("accepted creates: %+v", g.created)
	}
	eq(t, "labels sent", mustJSON(g.created[0].Labels), `["bug","area::x"]`)
	eq(t, "queue empty", mustJSON(len(e.app().loadOutbox())), "0")
}

func TestOutboxEditNewBodyKeepsLocalIDMarker(t *testing.T) {
	e := newEnv(t)
	e.run("new", "-R", "app", "--title", "t", "--body", "old")
	id := opIDOf(t, e, "new")
	if _, errs, code := e.run("outbox", "edit", id, "--body", "fresh text\n\n", "--title", "t2"); code != 0 {
		t.Fatal(errs)
	}
	args := e.app().loadOutbox()[0].Args
	eq(t, "body", argStr(args, "body"), "fresh text\n\n<!-- local-id: L-1 -->")
	eq(t, "title", argStr(args, "title"), "t2")
}

func TestOutboxEditRefusals(t *testing.T) {
	e := newEnv(t)
	seedLabels(e, "bug")
	e.run("new", "-R", "app", "--title", "t", "--body", "x")
	e.run("close", "app#7")
	e.run("comment", "app#7", "--body", "hello")
	newID, closeID, cID := opIDOf(t, e, "new"), opIDOf(t, e, "close"), opIDOf(t, e, "comment")
	for name, c := range map[string]struct {
		args []string
		msg  string
	}{
		"no field":       {[]string{"edit", newID}, "needs a field to change"},
		"wrong field":    {[]string{"edit", closeID, "--title", "x"}, "is a close write: it can change -, not --title"},
		"comment label":  {[]string{"edit", cID, "--label", "bug"}, "is a comment write: it can change body, not --label"},
		"unknown label":  {[]string{"edit", newID, "--label", "nope"}, `"nope"`},
		"blank title":    {[]string{"edit", newID, "--title", " "}, "title must not be empty"},
		"blank comment":  {[]string{"edit", cID, "--body", " ", "--allow-empty-body"}, "a comment needs a body"},
		"blank body":     {[]string{"edit", newID, "--body", ""}, "is empty"},
		"short id":       {[]string{"edit", "ab", "--title", "x"}, "too short"},
		"no such id":     {[]string{"drop", "zzzzzzzz"}, "no queued op matches"},
		"unknown subcmd": {[]string{"frobnicate"}, "unknown subcommand"},
	} {
		before := mustJSON(e.app().loadOutbox()[0].Args)
		_, errs, code := e.run(append([]string{"outbox"}, c.args...)...)
		if code == 0 || !strings.Contains(errs, c.msg) {
			t.Errorf("%s: code=%d err=%q want %q", name, code, errs, c.msg)
		}
		if after := mustJSON(e.app().loadOutbox()[0].Args); after != before {
			t.Errorf("%s: a refused edit changed the op: %s -> %s", name, before, after)
		}
	}
}

func TestOutboxAmbiguousIDListsCandidates(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.run("new", "-R", "app", "--title", "a", "--body", "x")
	e.run("new", "-R", "app", "--title", "b", "--body", "x")
	_, errs, code := e.run("outbox", "drop", "2026")
	if code != 2 || !strings.Contains(errs, `matches 2 ops`) || queued(e) != 2 {
		t.Fatalf("code=%d err=%q queued=%d", code, errs, queued(e))
	}
}

func TestOutboxDrop(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.run("new", "-R", "app", "--title", "a", "--body", "x")
	e.run("edit", "app#L-1", "--title", "a2") // queued on the issue that the create will make
	e.run("comment", "app#9", "--body", "other issue")
	newID, commentID := opIDOf(t, e, "new"), opIDOf(t, e, "comment")

	_, errs, code := e.run("outbox", "drop", newID)
	if code != 2 || !strings.Contains(errs, "creates app#L-1, and 1 queued write(s) act on it") || queued(e) != 3 {
		t.Fatalf("a create with dependents must not be dropped alone: code=%d err=%q queued=%d", code, errs, queued(e))
	}
	out, _, code := e.run("outbox", "drop", commentID)
	if code != 0 || out != "dropped "+commentID+"  comment app#9\n" || queued(e) != 2 {
		t.Fatalf("code=%d out=%q queued=%d", code, out, queued(e))
	}
	if _, errs, code = e.run("outbox", "drop", newID, "--cascade"); code != 0 || queued(e) != 0 {
		t.Fatalf("code=%d err=%q queued=%d", code, errs, queued(e))
	}
	if _, _, code = e.run("outbox", "drop", commentID); code != 1 { // already gone
		t.Fatalf("dropping twice must report that nothing matches, code %d", code)
	}
}

func TestOutboxRefusesWhileAPushIsRunning(t *testing.T) {
	e := newEnv(t)
	e.run("new", "-R", "app", "--title", "a", "--body", "x")
	id := opIDOf(t, e, "new")
	f, ok := e.app().mirror().tryPushLock()
	if !ok {
		t.Fatal("lock must be free")
	}
	defer f.Close()
	for _, args := range [][]string{{"drop", id}, {"edit", id, "--title", "z"}} {
		_, errs, code := e.run(append([]string{"outbox"}, args...)...)
		if code != 1 || !strings.Contains(errs, "a push is running") {
			t.Errorf("%v: code=%d err=%q", args, code, errs)
		}
	}
	eq(t, "untouched", argStr(e.app().loadOutbox()[0].Args, "title"), "a")
}

func TestOutboxLsJSONAndEmpty(t *testing.T) {
	e := newEnv(t)
	if out, _, code := e.run("outbox", "ls", "--format", "json"); code != 0 || out != "[]\n" {
		t.Fatalf("empty: %d %q", code, out)
	}
	if out, _, _ := e.run("outbox"); out != "" { // bare `outbox` lists, and an empty queue prints nothing in tsv
		t.Fatalf("%q", out)
	}
	e.run("new", "-R", "app", "--title", "a", "--body", "x", "-L", "bug")
	out, _, _ := e.run("outbox", "-j")
	var l []map[string]any
	if err := json.Unmarshal([]byte(out), &l); err != nil || len(l) != 1 {
		t.Fatalf("%v %q", err, out)
	}
	eq(t, "summary", l[0]["summary"].(string), "new: a [bug]")
	eq(t, "last_error", mustJSON(l[0]["last_error"]), "null")
	eq(t, "args labels", mustJSON(l[0]["args"].(map[string]any)["labels"]), `["bug"]`)
}

func TestOutboxEditLabelOpAddAndRemove(t *testing.T) {
	e := newEnv(t)
	seedLabels(e, "bug", "urgent")
	e.run("label", "app#7", "--add", "bug", "--remove", "old")
	id := opIDOf(t, e, "label")
	_, errs, code := e.run("outbox", "edit", id, "--add", "urgnt")
	if code != 2 || !strings.Contains(errs, `"urgnt" (did you mean urgent?)`) {
		t.Fatalf("an added label is checked: %d %q", code, errs)
	}
	if _, errs, code = e.run("outbox", "edit", id, "--add", "URGENT", "--remove", "stale-one", "--remove", "other"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	args := e.app().loadOutbox()[0].Args
	eq(t, "add", mustJSON(args["add"]), `["urgent"]`)
	eq(t, "remove (not checked, replaced)", mustJSON(args["remove"]), `["stale-one","other"]`)
	if _, _, code = e.run("outbox", "edit", id, "--remove", "x"); code != 0 {
		t.Fatal("--remove alone must work")
	}
	eq(t, "add kept when only remove changes", mustJSON(e.app().loadOutbox()[0].Args["add"]), `["urgent"]`)
}

func TestOutboxEditMilestone(t *testing.T) {
	e := newEnv(t)
	e.run("milestones", "new", "v1", "-R", "app") // M-1
	e.run("new", "-R", "app", "--title", "t", "--body", "x", "-m", "other")
	e.run("milestone", "app#7", "v9")
	newID, msID := opIDOf(t, e, "new"), opIDOf(t, e, "milestone")
	if _, errs, code := e.run("outbox", "edit", newID, "--milestone", "M-1"); code != 0 {
		t.Fatal(errs)
	}
	eq(t, "M-1 expands to the title", argStr(findOpByID(e, newID).Args, "milestone"), "v1")
	if _, errs, code := e.run("outbox", "edit", newID, "--milestone", "M-9"); code == 0 || !strings.Contains(errs, "M-9") {
		t.Fatalf("an unknown M-ref is refused: %d %q", code, errs)
	}
	for _, none := range []string{"none", ""} {
		e.run("outbox", "edit", newID, "--milestone", "v1")
		if _, errs, code := e.run("outbox", "edit", newID, "--milestone", none); code != 0 {
			t.Fatal(errs)
		}
		if m := findOpByID(e, newID).Args["milestone"]; m != nil {
			t.Fatalf("%q must clear the milestone of a queued create, got %v", none, m)
		}
	}
	if _, errs, code := e.run("outbox", "edit", msID, "--milestone", "v2"); code != 0 {
		t.Fatal(errs)
	}
	eq(t, "milestone op title", argStr(findOpByID(e, msID).Args, "title"), "v2")
}

func findOpByID(e *env, id string) *Op {
	for _, o := range e.app().loadOutbox() {
		if o.OpID == id {
			return o
		}
	}
	e.t.Fatalf("op %s is gone", id)
	return nil
}

func TestOutboxLsTableAndSummaryWithoutLabels(t *testing.T) {
	e := newEnv(t)
	e.tty, e.width = true, 120
	out, errs, _ := e.run("outbox", "ls")
	if out != "" || !strings.Contains(errs, "outbox is empty") {
		t.Fatalf("empty table: out=%q err=%q", out, errs)
	}
	e.run("new", "-R", "app", "--title", "plain", "--body", "x")
	out, _, _ = e.run("outbox", "ls", "--no-color")
	contains0(t, "header", out, "ID")
	contains0(t, "row", out, "app#L-1")
	contains0(t, "summary has no empty label list", out, "new: plain ")
	if strings.Contains(out, "[]") {
		t.Fatalf("a create without labels must not show []: %s", out)
	}
	eq(t, "short id", shortOpID("0123456789"), "23456789")
	eq(t, "short id of a short one", shortOpID("abc"), "abc")
}
