package issues

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestDependsOnParsing(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       []int
	}{
		{"one", "text\n\nDepends on #3", []int{3}},
		{"list", "Depends on #3, #5", []int{3, 5}},
		{"and, dot, case", "depends on #3 and #5.", []int{3, 5}},
		{"two lines, repeats", "Depends on #3\nmore\nDepends on #5, #3\n", []int{3, 5}},
		{"crlf", "Depends on #3\r\n", []int{3}},
		{"prose is not a dependency", "Depends on the auth service (see #4)", nil},
		{"trailing junk is not a dependency", "Depends on #3x", nil},
		{"other repo is not a dependency", "Depends on other/repo#3", nil},
		{"not at line start", "this Depends on #3", nil},
		{"none", "just text #3", nil},
	} {
		if got := dependsOn(c.body); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: dependsOn(%q) = %v, want %v", c.name, c.body, got, c.want)
		}
	}
}

func TestWithDepends(t *testing.T) {
	marker := "<!-- local-id: L-5 -->"
	for _, c := range []struct {
		name, body string
		nums       []int
		want       string
	}{
		{"append", "text\n", []int{3, 5}, "text\n\nDepends on #3, #5"},
		{"empty body", "", []int{3}, "Depends on #3"},
		{"replace in place", "a\n\nDepends on #1\n\nb\n", []int{2}, "a\n\nDepends on #2\n\nb\n"},
		{"remove", "a\n\nDepends on #1\nb\n", nil, "a\n\nb\n"},
		{"second line dropped", "Depends on #1\nx\nDepends on #2\n", []int{4}, "Depends on #4\nx\n"},
		{"above the local-id marker", "text\n\n" + marker, []int{3}, "text\n\nDepends on #3\n\n" + marker},
		{"nothing to remove", "text\n", nil, "text\n"},
		{"prose line is left alone", "Depends on the API\n", []int{3}, "Depends on the API\n\nDepends on #3"},
	} {
		got := withDepends(c.body, c.nums)
		eq(t, c.name, got, c.want)
		// the round trip through GitHub is the body text, so what is written must read back
		if back := dependsOn(got); !reflect.DeepEqual(back, c.nums) && !(len(back) == 0 && len(c.nums) == 0) {
			t.Errorf("%s: wrote %v, read back %v", c.name, c.nums, back)
		}
	}
}

// seedIssueBody mirrors issue n with the given state and body.
func seedIssueBody(e *env, n int, state, body string) {
	i := fixtureIssueNum(n)
	i.State, i.Body = state, body
	i.Comments.Nodes = nil
	e.putIssue("app", strconv.Itoa(n), renderIssue(i))
}

func reindex(e *env) {
	if err := e.app().rebuildIndex("app", nil, nil); err != nil {
		e.t.Fatal(err)
	}
}

// seedDeps: #3 and #5 open, #4 closed, #9 depends on #3 and #4, #10 only on the closed #4, #11 has none.
func seedDeps(e *env) {
	seedIssueBody(e, 3, "OPEN", "three")
	seedIssueBody(e, 4, "CLOSED", "four")
	seedIssueBody(e, 5, "OPEN", "five")
	seedIssueBody(e, 9, "OPEN", "nine\n\nDepends on #3, #4")
	seedIssueBody(e, 10, "OPEN", "ten\n\nDepends on #4")
	seedIssueBody(e, 11, "OPEN", "eleven")
	reindex(e)
}

func statusOf(t *testing.T, e *env, ref string) (status, detail string, deps string) {
	t.Helper()
	m := showJSONOf(t, e, ref)
	return m["status"].(string), m["status_detail"].(string), mustJSON(m["depends_on"])
}

func TestShowJSONDependsOnAndBlocked(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	st, detail, deps := statusOf(t, e, "app#9")
	eq(t, "status", st, "blocked")
	eq(t, "detail names only the open dependency", detail, "blocked: by #3")
	eq(t, "depends_on lists every dependency", deps, "[3,4]")
	st, _, deps = statusOf(t, e, "app#10")
	eq(t, "closed dependency does not block", st, "open")
	eq(t, "depends_on", deps, "[4]")
	st, _, deps = statusOf(t, e, "app#11")
	eq(t, "no dependency", st+deps, "open[]")
}

func TestLsJSONDependsOn(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	out, errs, code := e.run("ls", "--format", "json", "-S", "all")
	if code != 0 {
		t.Fatal(code, errs)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r["ref"].(string)] = mustJSON(r["depends_on"]) + " " + r["status"].(string)
	}
	eq(t, "app#9", got["app#9"], "[3,4] blocked")
	eq(t, "app#10", got["app#10"], "[4] open")
	eq(t, "app#11", got["app#11"], "[] open")
}

// Closing the dependency unblocks the dependent at once, from the queued write and again after the mirror
// has the closed state; reopening blocks it again.
func TestClosingADependencyUnblocksDependents(t *testing.T) {
	e := newEnv(t)
	e.tick = true // the outbox is ordered by timestamp: give close and reopen distinct ones
	seedDeps(e)
	eq(t, "before", firstOf(statusOf(t, e, "app#9")), "blocked")
	if _, errs, code := e.run("close", "app#3", "--agent", "x"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "queued close", firstOf(statusOf(t, e, "app#9")), "open")
	if _, errs, code := e.run("reopen", "app#3"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "queued reopen after close", firstOf(statusOf(t, e, "app#9")), "blocked")
	// the mirror catches up (as after sync + pull) and the outbox is empty
	for _, op := range e.app().loadOutbox() {
		e.app().mirror().removeOp(op)
	}
	seedIssueBody(e, 3, "CLOSED", "three")
	reindex(e)
	eq(t, "mirror closed", firstOf(statusOf(t, e, "app#9")), "open")
}

func firstOf(a, _, _ string) string { return a }

func TestUnlistedDependencyDoesNotBlock(t *testing.T) {
	e := newEnv(t)
	seedIssueBody(e, 3, "OPEN", "x\n\nDepends on #99")
	reindex(e)
	eq(t, "status", firstOf(statusOf(t, e, "app#3")), "open")
}

func TestNewDependsOn(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	out, errs, code := e.run("new", "-R", "app", "--title", "t", "--body", "hello", "--depends-on", "3", "--depends-on", "app#5")
	if code != 0 {
		t.Fatal(code, errs)
	}
	ops := e.app().loadOutbox()
	body := argStr(ops[0].Args, "body")
	eq(t, "queued body", body, "hello\n\nDepends on #3, #5\n\n<!-- local-id: L-1 -->")
	st, detail, deps := statusOf(t, e, refOf(t, out))
	eq(t, "pending issue is blocked", st+" | "+detail+" | "+deps, "blocked | blocked: by #3, #5 | [3,5]")
}

func TestNewDependsOnRefusals(t *testing.T) {
	for _, c := range []struct{ val, msg string }{
		{"lib#3", "same repo"},
		{"abc", "want an issue number"},
		{"0", "want an issue number"},
		{"#", "want an issue number"},
	} {
		e := newEnv(t)
		_, errs, code := e.run("new", "-R", "app", "--title", "t", "--body", "x", "--depends-on", c.val)
		if code != 2 || queued(e) != 0 {
			t.Fatalf("%s: code=%d queued=%d", c.val, code, queued(e))
		}
		contains0(t, c.val, errs, c.msg)
	}
}

func queuedBody(t *testing.T, e *env) string {
	t.Helper()
	ops := e.app().loadOutbox()
	if len(ops) != 1 {
		t.Fatalf("want 1 op, have %d", len(ops))
	}
	return argStr(ops[0].Args, "body")
}

func TestEditDependsOnKeepsTheBody(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	if _, errs, code := e.run("edit", "app#9", "--depends-on", "5"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "line replaced, rest kept", queuedBody(t, e), "nine\n\nDepends on #5\n")
	st, _, deps := statusOf(t, e, "app#9")
	eq(t, "show applies the queued edit", st+deps, "blocked[5]")
}

func TestEditDependsOnNoneClears(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	if _, errs, code := e.run("edit", "app#9", "--depends-on", "none"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "line gone", queuedBody(t, e), "nine\n\n")
	eq(t, "unblocked", firstOf(statusOf(t, e, "app#9")), "open")
}

func TestEditDependsOnWithBodyUsesTheGivenBody(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	if _, errs, code := e.run("edit", "app#11", "--body", "fresh", "--depends-on", "3"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "body", queuedBody(t, e), "fresh\n\nDepends on #3")
}

func TestEditDependsOnRefusals(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"--depends-on", "11"}, "cannot depend on itself"},
		{[]string{"--depends-on", "none", "--depends-on", "3"}, "do not combine"},
		{[]string{"--depends-on", "lib#3"}, "same repo"},
	} {
		_, errs, code := e.run(append([]string{"edit", "app#11"}, c.args...)...)
		if code != 2 || queued(e) != 0 {
			t.Fatalf("%v: code=%d queued=%d", c.args, code, queued(e))
		}
		contains0(t, strings.Join(c.args, " "), errs, c.msg)
	}
}

// A body that already holds a Depends-on line (written on GitHub) is what edit rewrites, whatever the spacing.
func TestEditDependsOnIsStableWhenUnchanged(t *testing.T) {
	e := newEnv(t)
	seedDeps(e)
	if _, errs, code := e.run("edit", "app#9", "--depends-on", "3", "--depends-on", "4"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "same set, same body", queuedBody(t, e), "nine\n\nDepends on #3, #4\n")
}
