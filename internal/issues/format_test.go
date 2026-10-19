package issues

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The on-disk format tests. The mirror is shared with a Python tool, so these read the files by path and compare
// bytes: file names, TSV columns, JSON key order, counter text. Every other test goes through the store's
// collections and does not care where or how they keep their data.

func TestOnDiskFormatCounters(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.run("new", "--repo", "app", "--title", "t", "--body", "b")
		e.run("milestones", "new", fmt.Sprintf("m%d", i), "-R", "app")
	}
	e.run("new", "--repo", "lib", "--title", "t", "--body", "b")
	eq(t, "app/local-seq", e.rawRead("app/local-seq"), "3\n")
	eq(t, "app/local-mseq", e.rawRead("app/local-mseq"), "3\n")
	eq(t, "lib/local-seq", e.rawRead("lib/local-seq"), "1\n")
}

func TestOnDiskFormatLocalmapAfterPush(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	g := e.useMem()
	g.repo("example/app").nextNum = 611
	e.run("new", "--repo", "app", "--title", "first thing", "--body", "b")
	if _, _, code := e.run("push"); code != 0 {
		t.Fatal("push")
	}
	eq(t, "localmap.tsv", e.rawRead("localmap.tsv"), "app\tL-1\t612\n")
}

func TestOnDiskFormatPulledFiles(t *testing.T) {
	e := newEnv(t)
	issue := map[string]any{"number": 5, "title": "t", "body": "b", "state": "CLOSED", "stateReason": "COMPLETED", "url": "u", "createdAt": "2026-02-01T00:00:00Z",
		"updatedAt": "2026-02-02T00:00:00Z", "id": "I5", "author": map[string]any{"login": "al"}, "milestone": map[string]any{"title": "v1"},
		"assignees": map[string]any{"nodes": []any{}}, "labels": map[string]any{"nodes": []any{map[string]any{"name": "bug"}}},
		"comments": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}}
	pr := map[string]any{"number": 6, "title": "p", "body": "", "state": "OPEN", "isDraft": true, "headRefName": "h", "headRefOid": "o", "baseRefName": "main",
		"createdAt": "2026-02-01T00:00:00Z", "mergedAt": nil, "mergeCommit": nil, "author": nil, "updatedAt": "2026-02-03T00:00:00Z", "url": "pu",
		"closingIssuesReferences": map[string]any{"nodes": []any{map[string]any{"number": 5}}}}
	e.useMem().cannedPull(t, "example/app", map[string]any{
		"labels": map[string]any{"nodes": []any{map[string]any{"name": "kind:bug", "color": "d73a4a", "description": "Something\tis broken"}}},
		"milestones": map[string]any{"nodes": []any{map[string]any{"number": 3, "title": "v1", "state": "OPEN", "dueOn": "2026-04-01T00:00:00Z",
			"description": "first\tline\nsecond", "updatedAt": "2026-02-01T00:00:00Z"}}},
		"issues":       map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{issue}},
		"pullRequests": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{pr}}})
	e.now = time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	if _, errs, code := e.run("pull", "--repo", "app"); code != 0 {
		t.Fatal(code, errs)
	}
	eq(t, "index.tsv", e.rawRead("app/index.tsv"), "5\tclosed\tv1\tbug\t2026-02-02T00:00:00Z\tt\n")
	eq(t, "cursor", e.rawRead("app/cursor"), "2026-02-02T00:00:00Z\n")
	eq(t, "lastpull", e.rawRead("app/lastpull"), "1770681600\n")
	eq(t, "milestones.tsv", e.rawRead("app/milestones.tsv"), "3\t\tv1\topen\t2026-04-01\tfirst\\tline\\nsecond\t2026-02-01T00:00:00Z\n")
	eq(t, "labels.tsv", e.rawRead("app/labels.tsv"), "kind:bug\td73a4a\tSomething is broken\n")
	eq(t, "prs.tsv", e.rawRead("app/prs.tsv"), "6\topen\t1\th\to\tmain\t2026-02-01T00:00:00Z\t\t\tghost\t2026-02-03T00:00:00Z\t5\tp\n")
	md := e.rawRead("app/5.md")
	if !strings.HasPrefix(md, "---\nnumber: 5\ntitle: \"t\"\nstate: \"closed\"\n") || !strings.Contains(md, "milestone: \"v1\"\n") {
		t.Fatalf("app/5.md front matter:\n%s", md)
	}
}

func TestOnDiskFormatClaimAndOutbox(t *testing.T) {
	e := newEnv(t)
	seedIndex(e)
	e.run("claim", "app#3", "--agent", "a1", "--ttl", "10m")
	c := e.rawRead("claims/app-3.json")
	for _, want := range []string{`{"agent": "a1", "pid": `, `"ts": 1772366400.0, "ttl": 600}`} {
		contains0(t, "claims/app-3.json", c, want)
	}
	e.run("comment", "app#3", "--body", "hi")
	keys := e.outboxKeys()
	if len(keys) != 1 {
		t.Fatalf("queued: %v", keys)
	}
	op := e.rawRead("outbox/" + keys[0] + ".json")
	if !strings.HasPrefix(op, `{"op_id": "`) || !strings.HasSuffix(op, `"op": "comment", "ref": "app#3", "args": {"body": "hi"}, "attempts": 0, "last_error": null}`+"\n") {
		t.Fatalf("outbox op: %q", op)
	}
}
