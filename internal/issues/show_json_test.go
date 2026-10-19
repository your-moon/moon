package issues

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func showJSONOf(t *testing.T, e *env, ref string) map[string]any {
	t.Helper()
	out, errs, code := e.run("show", ref, "--format", "json")
	if code != 0 {
		t.Fatalf("show %s: exit %d: %s", ref, code, errs)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	return m
}

func seedIssue7(e *env) {
	e.putIssue("app", "7", renderIssue(fixtureIssue()))
	if err := e.app().rebuildIndex("app", nil, nil); err != nil {
		e.t.Fatal(err)
	}
}

func TestShowJSONFields(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	m := showJSONOf(t, e, "app#7")
	want := map[string]any{
		"ref": "app#7", "number": 7.0, "state": "open", "status": "open", "milestone": nil,
		"labels": []any{"bug", "area::ui"}, "title": "widget: renders \"blank\" ünïcode\ttab", "updated_at": "2026-02-01T00:00:00Z",
		"body": "line one\nline two", "assignees": []any{}, "author": "ghost", "created_at": "2026-01-01T00:00:00Z",
		"url": "https://github.com/example/app/issues/7", "claim": nil, "pending": 0.0, "status_detail": "open", "depends_on": []any{},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got  %v\nwant %v", m, want)
	}
	// the comments are not part of the body
	if strings.Contains(m["body"].(string), "first") {
		t.Fatal("body contains a comment")
	}
}

func TestShowJSONClaimAndQueuedEdit(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	if _, _, code := e.run("claim", "app#7", "--agent", "alice", "--ttl", "30m"); code != 0 {
		t.Fatal(code)
	}
	if _, _, code := e.run("edit", "app#7", "--body", "new body"); code != 0 {
		t.Fatal(code)
	}
	m := showJSONOf(t, e, "app#7")
	eq(t, "body", m["body"].(string), "new body") // the queued edit is what show reports, as in the human view
	eq(t, "pending", mustJSON(m["pending"]), "1")
	cl := m["claim"].(map[string]any)
	eq(t, "claim agent", cl["agent"].(string), "alice")
	eq(t, "claim ttl", mustJSON(cl["ttl_seconds"]), "1800")
	eq(t, "claim stale", mustJSON(cl["stale"]), "false")
	eq(t, "status", m["status"].(string), "working")
}

func TestShowJSONLocalPendingIssue(t *testing.T) {
	e := newEnv(t)
	out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "hello", "-L", "bug")
	ref := refOf(t, out)
	m := showJSONOf(t, e, ref)
	eq(t, "ref", m["ref"].(string), "app#L-1")
	if m["number"] != nil || m["url"] != nil {
		t.Fatalf("a local issue has no number or url yet: %v", m)
	}
	eq(t, "labels", mustJSON(m["labels"]), `["bug"]`)
	contains0(t, "body", m["body"].(string), "hello")
}

func TestShowJSONUnknownIssueFails(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	out, errs, code := e.run("show", "app#99", "--format", "json")
	if code != 1 || out != "" || !strings.Contains(errs, "no issue app#99 in the mirror") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestShowJSONPicksTheRightQueuedIssueAndOps(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	e.run("new", "--repo", "app", "--title", "first", "--body", "one", "-m", "v1")
	e.run("new", "--repo", "app", "--title", "second", "--body", "two")
	e.run("edit", "app#L-1", "--body", "edited one") // an op for the other queued issue must not leak into L-2
	e.run("edit", "lib#7", "--body", "other repo")   // nor one with the same number in another repo
	m := showJSONOf(t, e, "app#L-2")
	eq(t, "title", m["title"].(string), "second")
	contains0(t, "body", m["body"].(string), "two")
	eq(t, "pending", mustJSON(m["pending"]), "0")
	eq(t, "milestone", mustJSON(m["milestone"]), "null")
	m = showJSONOf(t, e, "app#L-1")
	eq(t, "milestone", m["milestone"].(string), "v1")
	contains0(t, "body", m["body"].(string), "edited one")
	eq(t, "pending", mustJSON(m["pending"]), "1")
	eq(t, "other repo untouched", showJSONOf(t, e, "app#7")["body"].(string), "line one\nline two")
}

func TestShowJSONFollowsLandedLocalRef(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	e.mapLocal("app", "L-3", 7) // the create for L-3 landed as #7
	m := showJSONOf(t, e, "app#L-3")
	eq(t, "ref", m["ref"].(string), "app#7")
	eq(t, "number", mustJSON(m["number"]), "7")
}

func TestShowJSONLiveFetchForPrunedIssue(t *testing.T) {
	e := newEnv(t)
	e.putFile("app", fileIndex, "12\tclosed\t\tbug\t2025-01-01T00:00:00Z\told\n") // listed, file pruned
	g := e.useMem()
	g.put("example/app", memIssue{num: 12, title: "old", body: "from github", state: "CLOSED", labels: []string{"bug"}, assignees: []string{"sam"}})
	m := showJSONOf(t, e, "app#12")
	eq(t, "body", m["body"].(string), "from github")
	eq(t, "state", m["state"].(string), "closed")
	eq(t, "assignees", mustJSON(m["assignees"]), `["sam"]`)
	g.hook = func(call, repo, ident string) error { return &GhError{Msg: "offline"} }
	_, errs, code := e.run("show", "app#12", "--format", "json")
	if code != 1 || !strings.Contains(errs, "live fetch failed: offline") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

// A `new` that is still queued wins over a localmap entry: until the op is gone the issue is the queued one.
func TestShowJSONQueuedNewBeatsLocalmap(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	e.run("new", "--repo", "app", "--title", "first", "--body", "one")
	e.run("new", "--repo", "app", "--title", "second", "--body", "two")
	e.mapLocal("app", "L-1", 7)
	eq(t, "queued L-1 stays local", showJSONOf(t, e, "app#L-1")["ref"].(string), "app#L-1")
	for _, op := range e.app().loadOutbox() {
		if op.Ref == "app#L-1" {
			e.app().mirror().removeOp(op)
		}
	}
	// L-2 is still queued, which must not stop L-1 from resolving
	eq(t, "landed L-1 follows", showJSONOf(t, e, "app#L-1")["ref"].(string), "app#7")
}
