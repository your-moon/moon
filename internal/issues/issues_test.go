package issues

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixtureIssue() *issueNode {
	n := &issueNode{Number: 7, Title: "widget: renders \"blank\" ünïcode\ttab", Body: "line one\r\nline two\n\n", State: "OPEN",
		URL: "https://github.com/example/app/issues/7", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-02-01T00:00:00Z", ID: "I_7"}
	n.Labels.Nodes = append(n.Labels.Nodes, struct {
		Name string `json:"name"`
	}{"bug"}, struct {
		Name string `json:"name"`
	}{"area::ui"})
	n.Comments.Nodes = []comment{{DatabaseID: 11, CreatedAt: "2026-01-02T00:00:00Z", Body: "first\r\n"}}
	return n
}

func TestRenderParseRoundTrip(t *testing.T) {
	e := newEnv(t)
	text := renderIssue(fixtureIssue())
	want := "---\nnumber: 7\ntitle: \"widget: renders \\\"blank\\\" ünïcode\\ttab\"\nstate: \"open\"\nstate_reason: null\nlabels: [\"bug\", \"area::ui\"]\nmilestone: null\nassignees: []\nauthor: \"ghost\"\ncreated_at: \"2026-01-01T00:00:00Z\"\nupdated_at: \"2026-02-01T00:00:00Z\"\nurl: \"https://github.com/example/app/issues/7\"\n---\n\nline one\nline two\n\n## Comments\n\n### ghost · 2026-01-02T00:00:00Z · id 11\n\nfirst\n"
	eq(t, "render", text, want)
	e.write("app/7.md", text)
	h := readHeader(filepath.Join(e.home, "app/7.md"))
	eq(t, "title", hStr(h, "title"), "widget: renders \"blank\" ünïcode\ttab")
	eq(t, "labels", strings.Join(hList(h, "labels"), ","), "bug,area::ui")
	a := e.app()
	if err := a.rebuildIndex("app", nil); err != nil {
		t.Fatal(err)
	}
	eq(t, "index", e.read("app/index.tsv"), "7\topen\t\tbug,area::ui\t2026-02-01T00:00:00Z\twidget: renders \"blank\" ünïcode tab\n")
	if r := a.readIndex("app"); len(r) != 1 || r[0][0] != "7" {
		t.Fatalf("readIndex %v", r)
	}
}

func TestPRRoundTrip(t *testing.T) {
	n := &prNode{Number: 3, Title: "fix\tthing", Body: "b\n", State: "MERGED", HeadRefName: "fix-1", HeadRefOid: "abc", BaseRefName: "main",
		CreatedAt: "2026-01-01T00:00:00Z", MergedAt: "2026-01-02T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", URL: "https://github.com/example/app/pull/3"}
	n.ClosingIssuesReferences.Nodes = append(n.ClosingIssuesReferences.Nodes, struct {
		Number int `json:"number"`
	}{7})
	r := prRowOf(n)
	e := newEnv(t)
	e.write("app/prs.tsv", r.tsv())
	got := e.app().readPRs("app")["3"]
	if got != r || got.Title != "fix thing" || got.MergeCommit != "" {
		t.Fatalf("row %+v", got)
	}
	if !strings.Contains(renderPR(n, r), "closes: [7]\n") || !strings.Contains(renderPR(n, r), "merge_commit: null\n") {
		t.Fatal(renderPR(n, r))
	}
	out, _, code := e.run("prs", "--state", "merged", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var l []map[string]any
	if err := json.Unmarshal([]byte(out), &l); err != nil || len(l) != 1 || l[0]["ref"] != "app#3" || l[0]["url"] != "https://github.com/example/app/pull/3" {
		t.Fatalf("%s %v", out, err)
	}
}

func TestConfig(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "moon.toml")
	os.WriteFile(p, []byte("# c\n[repos.one]\ngithub = \"o/one\" # trailing\n"), 0o644)
	c, err := LoadConfig(p)
	if err != nil || len(c.Repos) != 1 || c.Repos[0].GitHub != "o/one" || c.MirrorDir != filepath.Join(c.Dir, ".moon", "issues") {
		t.Fatalf("%+v %v", c, err)
	}
	sub := filepath.Join(d, "a", "b")
	os.MkdirAll(sub, 0o755)
	got, err := FindConfig("", sub, func(string) string { return "" })
	if err != nil || got != p {
		t.Fatalf("walk up: %q %v", got, err)
	}
	os.WriteFile(p, []byte("[repos.Bad]\ngithub = \"o/x\"\n"), 0o644)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("bad alias accepted")
	}
	os.WriteFile(p, []byte("[repos.x]\n"), 0o644)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("missing github accepted")
	}
}

func seedIndex(e *env) {
	e.write("app/index.tsv", "3\topen\tv1\tbug\t2026-02-01T00:00:00Z\tthird\n2\topen\t\t\t2026-02-01T00:00:00Z\tsecond\n1\tclosed\t\t\t2026-02-01T00:00:00Z\tfirst\n")
}

func TestClaimTTLAndFrontier(t *testing.T) {
	e := newEnv(t)
	seedIndex(e)
	out, _, _ := e.run("frontier")
	eq(t, "frontier all", out, "app#3\topen\tv1\tbug\tthird\napp#2\topen\t-\t-\tsecond\n")
	out, _, code := e.run("claim", "app#3", "--agent", "a1", "--ttl", "10m")
	eq(t, "claim", out, "claimed app#3 for a1\n")
	if code != 0 {
		t.Fatal(code)
	}
	out, _, _ = e.run("frontier")
	eq(t, "frontier hides fresh", out, "app#2\topen\t-\t-\tsecond\n")
	// another agent is refused with exit 3
	out, _, code = e.run("claim", "app#3", "--agent", "a2")
	if code != 3 || !strings.HasPrefix(out, "TAKEN app#3 is held by a1 (pid - on ") {
		t.Fatalf("%d %q", code, out)
	}
	// same agent renews
	out, _, _ = e.run("claim", "app#3", "--agent", "a1", "--ttl", "10m")
	eq(t, "renew", out, "renewed claim on app#3 for a1\n")
	// just inside the ttl: still fresh
	e.now = e.now.Add(10*time.Minute - time.Second)
	if _, _, code = e.run("claim", "app#3", "--agent", "a2"); code != 3 {
		t.Fatal("claim must still be fresh at ttl-1s")
	}
	// past the ttl: stale, shows up in frontier and can be taken over
	e.now = e.now.Add(2 * time.Second)
	out, _, _ = e.run("frontier")
	eq(t, "frontier shows stale", out, "app#3\topen\tv1\tbug\tthird\napp#2\topen\t-\t-\tsecond\n")
	out, _, _ = e.run("claims")
	contains0(t, "claims", out, "app#3\tSTALE\ta1\t")
	out, _, code = e.run("claim", "app#3", "--agent", "a2")
	if code != 0 || !strings.HasPrefix(out, "took over stale claim on app#3 (was a1 ") || !strings.HasSuffix(out, "now held by a2\n") {
		t.Fatalf("%d %q", code, out)
	}
	// release rules
	out, _, code = e.run("release", "app#3", "--agent", "a1")
	if code != 3 || out != "app#3: held by a2\n" {
		t.Fatalf("%d %q", code, out)
	}
	out, _, code = e.run("release", "app#3", "--agent", "a2")
	if code != 0 || out != "app#3: released\n" {
		t.Fatalf("%d %q", code, out)
	}
	out, _, code = e.run("release", "app#3", "--force")
	if code != 0 || out != "app#3: none\n" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestClaimDeadPidIsStale(t *testing.T) {
	e := newEnv(t)
	a := e.app()
	host, _ := os.Hostname()
	a.Host = host
	// pid 2^22+ is far above any live pid on a dev machine
	c := &Claim{"x", 4000000, host, a.nowEpoch(), 3600}
	if !a.claimIsStale(c) {
		t.Fatal("dead pid on this host must be stale")
	}
	c.Pid = os.Getpid()
	if a.claimIsStale(c) {
		t.Fatal("live pid must be fresh")
	}
	c.Host = "elsewhere"
	c.Pid = 4000000
	if a.claimIsStale(c) {
		t.Fatal("pid is only checked on the same host")
	}
}

func TestClaimFileFormat(t *testing.T) {
	e := newEnv(t)
	e.run("claim", "app#5", "--agent", "bot", "--ttl", "2h", "--pid", "0")
	s := e.read("claims/app-5.json")
	if !strings.HasPrefix(s, `{"agent": "bot", "pid": 0, "host": "`) || !strings.HasSuffix(s, `, "ttl": 7200}`+"\n") {
		t.Fatal(s)
	}
	// the Python tool's float ts parses
	e.write("claims/app-6.json", `{"agent": "py", "pid": 0, "host": "h", "ts": 1772000000.123456, "ttl": 60}`+"\n")
	out, _, _ := e.run("claims")
	contains0(t, "claims", out, "app#6\tSTALE\tpy\th\tpid=-\t")
}

func TestQueueReplay(t *testing.T) {
	serialPush(t)
	e := newEnv(t)
	e.tick = true
	seedIndex(e)
	// queue: a create, then edits on its local ref, a comment and a close on a real issue
	out, _, _ := e.run("new", "--repo", "app", "--title", "thing: broken", "--body", "hello\n\n", "--label", "bug", "--label", "x")
	f := strings.Fields(out)
	ref := f[3]
	if !strings.HasPrefix(ref, "app#L-") {
		t.Fatal(out)
	}
	e.run("comment", ref, "--body", "note one")
	e.run("comment", "app#2", "--body", "note two")
	e.run("close", "app#2", "--reason", "not_planned", "--comment", "dup")
	out, _, _ = e.run("ls", "--repo", "app")
	contains0(t, "ls pending", out, ref+"\topen\t-\tbug,x\tthing: broken (pending sync)")
	out, _, _ = e.run("status")
	contains0(t, "status", out, "outbox=4 errors=0")

	posted := []string{}
	e.gh.handler = func(args []string, stdin string) (string, error) {
		switch {
		case args[0] == "api" && strings.Contains(args[1], "/issues?state=all"):
			return "", nil
		case args[0] == "issue" && args[1] == "list":
			return "[]", nil
		case args[0] == "issue" && args[1] == "create":
			posted = append(posted, "create "+strings.Join(args[2:], " "))
			return "https://github.com/example/app/issues/40\n", nil
		}
		posted = append(posted, strings.Join(args[:3], " "))
		return "", nil
	}
	out, _, code := e.run("push")
	eq(t, "push", out, "PUSH pushed=4 failed=0\n")
	if code != 0 {
		t.Fatal(code)
	}
	if len(posted) != 4 || !strings.HasPrefix(posted[0], "create -R example/app --title thing: broken --body-file - --label bug --label x") ||
		posted[1] != "issue comment 2" || posted[2] != "issue comment 40" || posted[3] != "issue close 2" {
		t.Fatalf("order: %v", posted)
	}
	ents, _ := os.ReadDir(filepath.Join(e.home, "outbox"))
	if len(ents) != 0 {
		t.Fatalf("outbox not drained: %d", len(ents))
	}
	contains0(t, "localmap", e.read("localmap.tsv"), "\t40\n")
	// a later op on the old local ref resolves through localmap
	_, _, code = e.run("comment", ref, "--body", "late")
	if code != 0 {
		t.Fatal("late comment on landed local ref")
	}
	ops := e.app().loadOutbox()
	if len(ops) != 1 || ops[0].Ref != "app#40" {
		t.Fatalf("%+v", ops)
	}
}

func TestQueueFailureKeepsOrderAndRetries(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.write("app/2.md", renderIssue(fixtureIssueNum(2)))
	seedIndex(e)
	e.run("comment", "app#2", "--body", "one")
	e.run("label", "app#2", "--add", "bug")
	e.run("comment", "app#3", "--body", "other issue")
	fail := true
	e.gh.handler = func(args []string, stdin string) (string, error) {
		if fail && args[0] == "issue" && args[1] == "comment" && args[2] == "2" {
			return "", &GhError{Msg: "boom"}
		}
		return "", nil
	}
	out, _, code := e.run("push")
	// the failing ref is skipped for the rest of the run; the other ref still goes
	eq(t, "push1", out, "PUSH pushed=1 failed=1\n")
	if code != 1 {
		t.Fatal(code)
	}
	if e.gh.count("issue", "edit") != 0 {
		t.Fatal("label op on the same ref must not jump ahead of the failed comment")
	}
	ops := e.app().loadOutbox()
	if len(ops) != 2 || ops[0].Attempts != 1 || ops[0].LastError == nil || *ops[0].LastError != "boom" || ops[0].Inflight {
		t.Fatalf("%+v", ops[0])
	}
	out, _, _ = e.run("status")
	contains0(t, "status", out, "outbox=2 errors=1")
	contains0(t, "status", out, "attempts=1: boom")
	out, _, _ = e.run("show", "app#2")
	contains0(t, "show", out, "[last error: boom]")
	fail = false
	out, _, code = e.run("push")
	eq(t, "push2", out, "PUSH pushed=2 failed=0\n")
	if code != 0 || len(e.app().loadOutbox()) != 0 {
		t.Fatal("replay did not drain")
	}
}

func TestCommentReplayAfterTimeoutDoesNotDoublePost(t *testing.T) {
	e := newEnv(t)
	e.run("comment", "app#2", "--body", "once")
	op := e.app().loadOutbox()[0]
	posts := 0
	e.gh.handler = func(args []string, stdin string) (string, error) {
		if args[0] == "issue" && args[1] == "comment" {
			posts++
			return "", &GhError{Msg: "timeout after 90s: gh issue comment", Timeout: true}
		}
		return "", nil
	}
	e.run("push")
	if posts != 1 {
		t.Fatal(posts)
	}
	ops := e.app().loadOutbox()
	if !ops[0].Inflight {
		t.Fatal("a timeout must leave the op marked inflight")
	}
	// next run: the comment is already on GitHub (the timed-out call did land)
	e.gh.handler = func(args []string, stdin string) (string, error) {
		if args[0] == "api" {
			return "once\n\n<!-- op-id: " + op.OpID + " -->\n", nil
		}
		posts++
		return "", nil
	}
	out, _, _ := e.run("push")
	eq(t, "push", out, "PUSH pushed=1 failed=0\n")
	if posts != 1 {
		t.Fatalf("comment re-posted: %d", posts)
	}
}

func TestCreateIsIdempotentByMarker(t *testing.T) {
	e := newEnv(t)
	out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	ref := strings.Fields(out)[3]
	lid := strings.TrimPrefix(ref, "app#")
	// a mirror file already carries the marker: the create must not call gh
	e.write("app/55.md", "---\nnumber: 55\n---\n\nbody <!-- local-id: "+lid+" -->\n")
	e.gh.handler = func(args []string, stdin string) (string, error) { return "", &GhError{Msg: "must not be called"} }
	out, _, _ = e.run("push")
	eq(t, "push", out, "PUSH pushed=1 failed=0\n")
	if len(e.gh.calls) != 0 {
		t.Fatalf("gh called: %v", e.gh.calls)
	}
	contains0(t, "localmap", e.read("localmap.tsv"), lid+"\t55\n")
}

func TestShowOverlaysPending(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.write("app/2.md", renderIssue(fixtureIssueNum(2)))
	seedIndex(e)
	e.run("label", "app#2", "--add", "new", "--remove", "bug")
	e.run("milestone", "app#2", "v9")
	out, _, _ := e.run("show", "app#2")
	contains0(t, "show", out, "claim: none\nPENDING SYNC: 2 local op(s) not on GitHub yet; effective state: open | labels: area::ui,new | milestone: v9 | title: ")
	contains0(t, "show", out, "- (pending sync) label +new -bug\n- (pending sync) milestone -> v9\n")
	_, errs, code := e.run("show", "app#999")
	if code != 1 || !strings.Contains(errs, "app#999 not in the local mirror") {
		t.Fatal(code, errs)
	}
}

func fixtureIssueNum(n int) *issueNode {
	i := fixtureIssue()
	i.Number = n
	return i
}

func TestRefAndFlagErrors(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"show", "nope#1"}, {"show", "erp#1"}, {"ls", "--repo", "zzz"}, {"claim", "app#1"}, {"ls", "--bogus"}, {"claim", "app#1", "--agent", "a", "--ttl", "x"}} {
		if _, errs, code := e.run(args...); code != 2 || errs == "" {
			t.Fatalf("%v: code %d err %q", args, code, errs)
		}
	}
}

func TestPullWithFakeGitHub(t *testing.T) {
	e := newEnv(t)
	issue := map[string]any{"number": 5, "title": "t", "body": "b", "state": "CLOSED", "stateReason": "COMPLETED", "url": "u", "createdAt": "2026-02-01T00:00:00Z",
		"updatedAt": "2026-02-02T00:00:00Z", "id": "I5", "author": map[string]any{"login": "al"}, "milestone": map[string]any{"title": "v1"},
		"assignees": map[string]any{"nodes": []any{}}, "labels": map[string]any{"nodes": []any{map[string]any{"name": "bug"}}},
		"comments": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}}
	pr := map[string]any{"number": 6, "title": "p", "body": "", "state": "OPEN", "isDraft": true, "headRefName": "h", "headRefOid": "o", "baseRefName": "main",
		"createdAt": "2026-02-01T00:00:00Z", "mergedAt": nil, "mergeCommit": nil, "author": nil, "updatedAt": "2026-02-03T00:00:00Z", "url": "pu",
		"closingIssuesReferences": map[string]any{"nodes": []any{map[string]any{"number": 5}}}}
	e.gh.handler = func(args []string, stdin string) (string, error) {
		var req struct{ Query string }
		json.Unmarshal([]byte(stdin), &req)
		if strings.Contains(req.Query, "pullRequests(") {
			return mustJSON(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{pr}}}}}), nil
		}
		return mustJSON(map[string]any{"data": map[string]any{"repository": map[string]any{
			"milestones": map[string]any{"nodes": []any{map[string]any{"number": 3, "title": "v1", "state": "OPEN", "dueOn": "2026-04-01T00:00:00Z", "description": "first\tline\nsecond", "updatedAt": "2026-02-01T00:00:00Z"}}},
			"issues":     map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{issue}}}}}), nil
	}
	e.now = time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	out, _, code := e.run("pull", "--repo", "app")
	eq(t, "pull", out, "PULL app=1\n")
	if code != 0 {
		t.Fatal(code)
	}
	eq(t, "index", e.read("app/index.tsv"), "5\tclosed\tv1\tbug\t2026-02-02T00:00:00Z\tt\n")
	eq(t, "cursor", e.read("app/cursor"), "2026-02-02T00:00:00Z\n")
	eq(t, "milestones", e.read("app/milestones.tsv"), "3\t\tv1\topen\t2026-04-01\tfirst\\tline\\nsecond\t2026-02-01T00:00:00Z\n")
	eq(t, "prs", e.read("app/prs.tsv"), "6\topen\t1\th\to\tmain\t2026-02-01T00:00:00Z\t\t\tghost\t2026-02-03T00:00:00Z\t5\tp\n")
	out, _, _ = e.run("pull", "--repo", "app")
	eq(t, "idempotent pull", out, "PULL app=0\n")
	out, _, _ = e.run("prs", "--repo", "app")
	eq(t, "prs", out, "app#6\topen [draft]\th\tp\n")
	// keep-days: a closed issue older than the window loses its file but keeps its index row
	e.now = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	out, _, _ = e.run("prune", "--repo", "app")
	eq(t, "prune", out, "PRUNE removed app=1\n")
	if e.exists("app/5.md") || !strings.Contains(e.read("app/index.tsv"), "5\tclosed") {
		t.Fatal("prune must drop the file and keep the row")
	}
	// a claim guards the file from pruning
	e.write("app/5.md", renderIssue(fixtureIssueNum(5)))
	e.write("app/5.md", strings.Replace(e.read("app/5.md"), `state: "open"`, `state: "closed"`, 1))
	e.run("claim", "app#5", "--agent", "g")
	out, _, _ = e.run("prune", "--repo", "app")
	eq(t, "prune guarded", out, "PRUNE removed app=0\n")
}

func TestAutosyncSpawnsOnceWhenStale(t *testing.T) {
	e := newEnv(t)
	e.extra["ISSUES_NO_AUTOSYNC"] = ""
	spawn := filepath.Join(t.TempDir(), "spawn.log")
	e.extra["ISSUES_TEST_SPAWN"] = spawn
	seedIndex(e)
	e.write("app/lastpull", "1772366400\n")
	e.write("lib/lastpull", "1772366400\n")
	e.now = time.Unix(1772366400+601, 0)
	_, errs, _ := e.run("ls")
	contains0(t, "stderr", errs, "(mirror 10 min old; refreshing in background)")
	_, errs, _ = e.run("ls") // marker held: no second spawn
	eq(t, "second", errs, "")
	b, _ := os.ReadFile(spawn)
	eq(t, "spawns", string(b), "spawn\n")
	// fresh mirror: no spawn
	os.Remove(filepath.Join(e.home, ".autosync"))
	e.now = time.Unix(1772366400+60, 0)
	_, errs, _ = e.run("ls")
	eq(t, "fresh", errs, "")
}

func TestFindConfigOrder(t *testing.T) {
	mk := func(p string) string {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("[repos.a]\ngithub = \"o/a\"\n"), 0o644)
		return p
	}
	type tc struct {
		name                          string
		flag, env, walk, dotmoon, xdg bool
		useHome                       bool // XDG_CONFIG_HOME unset, file under HOME/.config
		want                          string
	}
	cases := []tc{
		{name: "flag beats all", flag: true, env: true, walk: true, dotmoon: true, xdg: true, want: "flag"},
		{name: "env beats walk", env: true, walk: true, dotmoon: true, xdg: true, want: "env"},
		{name: "walk beats xdg", walk: true, dotmoon: true, xdg: true, want: "walk"},
		{name: "moon.toml beats .moon/moon.toml", walk: true, dotmoon: true, want: "walk"},
		{name: ".moon/moon.toml beats xdg", dotmoon: true, xdg: true, want: "dotmoon"},
		{name: "xdg last", xdg: true, want: "xdg"},
		{name: "home default", xdg: true, useHome: true, want: "xdg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			proj := filepath.Join(root, "proj")
			cwd := filepath.Join(proj, "a", "b")
			os.MkdirAll(cwd, 0o755)
			home := filepath.Join(root, "home")
			xdgDir := filepath.Join(root, "xdgdir")
			paths := map[string]string{}
			flag, env := "", ""
			if c.flag {
				paths["flag"] = mk(filepath.Join(root, "flag.toml"))
				flag = paths["flag"]
			}
			if c.env {
				paths["env"] = mk(filepath.Join(root, "env.toml"))
				env = paths["env"]
			}
			if c.walk {
				paths["walk"] = mk(filepath.Join(proj, "moon.toml"))
			}
			if c.dotmoon {
				paths["dotmoon"] = mk(filepath.Join(proj, ".moon", "moon.toml"))
			}
			vars := map[string]string{"MOON_CONFIG": env, "HOME": home}
			if c.xdg {
				if c.useHome {
					paths["xdg"] = mk(filepath.Join(home, ".config", "moon", "moon.toml"))
				} else {
					paths["xdg"] = mk(filepath.Join(xdgDir, "moon", "moon.toml"))
					vars["XDG_CONFIG_HOME"] = xdgDir
				}
			}
			got, err := FindConfig(flag, cwd, func(k string) string { return vars[k] })
			if err != nil || got != paths[c.want] {
				t.Fatalf("got %q %v, want %q", got, err, paths[c.want])
			}
		})
	}
}

func TestFindConfigNotFound(t *testing.T) {
	root := t.TempDir()
	xdg := filepath.Join(root, "xdg")
	cwd := filepath.Join(root, "work")
	os.MkdirAll(cwd, 0o755)
	vars := map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": filepath.Join(root, "home")}
	_, err := FindConfig("", cwd, func(k string) string { return vars[k] })
	if err == nil {
		t.Fatal("want error")
	}
	m := err.Error()
	last := -1
	for _, w := range []string{"--config", "$MOON_CONFIG", ".moon/moon.toml", cwd, filepath.Join(xdg, "moon", "moon.toml"), "[repos.app]", "github ="} {
		i := strings.Index(m, w)
		if i < 0 || i < last {
			t.Fatalf("%q missing or out of order in:\n%s", w, m)
		}
		last = i
	}
	vars = map[string]string{}
	_, err = FindConfig("", cwd, func(k string) string { return vars[k] })
	if err == nil || !strings.Contains(err.Error(), "~/.config/moon/moon.toml") {
		t.Fatalf("no-home error: %v", err)
	}
}

func TestDotMoonMirrorDefault(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, ".moon", "moon.toml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("[repos.a]\ngithub = \"o/a\"\n"), 0o644)
	c, err := LoadConfig(p)
	if err != nil || c.MirrorDir != filepath.Join(d, ".moon", "issues") {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestWriteKicksBackgroundPush(t *testing.T) {
	e := newEnv(t)
	e.extra["ISSUES_NO_AUTOSYNC"] = ""
	spawn := filepath.Join(t.TempDir(), "spawn.log")
	e.extra["ISSUES_TEST_SPAWN"] = spawn
	seedIndex(e)
	if _, errs, code := e.run("comment", "app#9", "--body", "hi"); code != 0 {
		t.Fatalf("comment: %d %q", code, errs)
	}
	b, _ := os.ReadFile(spawn)
	eq(t, "first write spawns", string(b), "spawn\n")
	// a second write while that sync runs leaves a note instead of a second process
	e.run("comment", "app#9", "--body", "again")
	b, _ = os.ReadFile(spawn)
	eq(t, "no second spawn", string(b), "spawn\n")
	if !e.exists(".autosync.again") {
		t.Fatal("a write during a running sync must leave .autosync.again")
	}
	// reads do not kick a push
	os.Remove(filepath.Join(e.home, ".autosync"))
	os.Remove(filepath.Join(e.home, ".autosync.again"))
	e.write("app/lastpull", strconv.FormatInt(e.now.Unix(), 10)+"\n")
	e.write("lib/lastpull", strconv.FormatInt(e.now.Unix(), 10)+"\n")
	e.run("ls")
	b, _ = os.ReadFile(spawn)
	eq(t, "read: no spawn", string(b), "spawn\n")
}
