package issues

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// refOf pulls "app#L-3" out of `op <id>  ref <ref>` or `ref <ref>  (#n)`.
func refOf(t *testing.T, out string) string {
	t.Helper()
	f := strings.Fields(out)
	for i, w := range f {
		if w == "ref" && i+1 < len(f) {
			return f[i+1]
		}
	}
	t.Fatalf("no ref in %q", out)
	return ""
}

func seqOfRef(t *testing.T, ref string) int {
	t.Helper()
	_, id, _ := strings.Cut(ref, "#")
	n, ok := seqOf(id)
	if !ok {
		t.Fatalf("not a sequential ref: %q", ref)
	}
	return n
}

func TestSequentialLocalIds(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 3; i++ {
		out, _, code := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
		if code != 0 || refOf(t, out) != fmt.Sprintf("app#L-%d", i) {
			t.Fatalf("%d: %q", i, out)
		}
	}
	eq(t, "counter", e.read("app/local-seq"), "3\n")
	out, _, _ := e.run("new", "--repo", "lib", "--title", "t", "--body", "b") // counters are per alias
	eq(t, "lib ref", refOf(t, out), "lib#L-1")
	// the body keeps the idempotency marker with the short id
	for _, op := range e.app().loadOutbox() {
		if op.Ref == "app#L-1" && (!strings.Contains(argStr(op.Args, "body"), "<!-- local-id: L-1 -->") || argStr(op.Args, "local_id") != "L-1") {
			t.Fatalf("%+v", op.Args)
		}
	}
}

func TestConcurrentAllocationGoroutines(t *testing.T) {
	e := newEnv(t)
	const workers, per = 8, 25
	var mu sync.Mutex
	var wg sync.WaitGroup
	got := []int{}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := e.app()
			for i := 0; i < per; i++ {
				n, err := a.nextLocalID("app", famIssue)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				got = append(got, n)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assertContiguous(t, got, workers*per)
}

func assertContiguous(t *testing.T, got []int, n int) {
	t.Helper()
	sort.Ints(got)
	if len(got) != n {
		t.Fatalf("got %d numbers, want %d", len(got), n)
	}
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("not distinct and contiguous at %d: %v", i, got)
		}
	}
}

func TestConcurrentAllocationProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "moon")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/moon")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	e := newEnv(t)
	const procs, per = 4, 15
	results := make(chan string, procs*per)
	var wg sync.WaitGroup
	for p := 0; p < procs; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				cmd := exec.Command(bin, "issues", "--config", e.cfg, "new", "--repo", "app", "--title", "t", "--body", "b")
				cmd.Env = append(os.Environ(), "ISSUES_NO_AUTOSYNC=1")
				out, err := cmd.Output()
				if err != nil {
					t.Errorf("new: %v", err)
					return
				}
				results <- string(out)
			}
		}()
	}
	wg.Wait()
	close(results)
	var got []int
	for out := range results {
		got = append(got, seqOfRef(t, refOf(t, out)))
	}
	assertContiguous(t, got, procs*per)
	eq(t, "counter", e.read("app/local-seq"), strconv.Itoa(procs*per)+"\n")
}

func TestNumbersNeverReused(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	for i := 0; i < 3; i++ {
		e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	}
	e.run("close", "app#L-3") // closing the highest must not free its number
	out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	eq(t, "after close", refOf(t, out), "app#L-4")

	// pushing lands them in localmap and drains the outbox; the numbers stay taken
	e.gh.handler = func(args []string, stdin string) (string, error) {
		switch {
		case args[0] == "api":
			return "", nil
		case args[0] == "issue" && args[1] == "list":
			return "[]", nil
		case args[0] == "issue" && args[1] == "create":
			return "https://github.com/example/app/issues/" + strconv.Itoa(100+e.gh.callCount()) + "\n", nil
		}
		return "", nil
	}
	if o, _, code := e.run("push"); code != 0 {
		t.Fatalf("push %d %s", code, o)
	}
	out, _, _ = e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	eq(t, "after push", refOf(t, out), "app#L-5")

	// a lost counter is rebuilt from the outbox and localmap, never restarted at 1
	os.Remove(filepath.Join(e.home, "app", "local-seq"))
	out, _, _ = e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	eq(t, "lost counter", refOf(t, out), "app#L-6")
}

func TestLegacyIdsStayValid(t *testing.T) {
	e := newEnv(t)
	a := e.app()
	for _, ok := range []string{"12", "L-1", "L-0042", "L-20261015194409-e10b"} {
		if !validIdent(ok) {
			t.Errorf("%q should be valid", ok)
		}
		if _, _, err := a.parseRef("app#" + ok); err != nil {
			t.Errorf("parseRef %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "L-", "L-x", "L-1x", "L-1-2", "l-1", "L-20261015194409-E10B", "L-2026101519440-e10b", "L-20261015194409-e10"} {
		if validIdent(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	// a legacy ref keeps resolving through localmap, and its files and ids are never rewritten
	e.write("localmap.tsv", "app\tL-20261015194409-e10b\t40\n")
	e.write("app/40.md", renderIssue(fixtureIssueNum(40)))
	out, _, code := e.run("show", "app#L-20261015194409-e10b")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	contains0(t, "legacy show", out, "ref: L-20261015194409-e10b (now #40)\n")
	eq(t, "localmap untouched", e.read("localmap.tsv"), "app\tL-20261015194409-e10b\t40\n")
	// a legacy create still sitting in the outbox keeps working next to new-style ids
	_, _, code = e.run("comment", "app#L-20261015194409-e10b", "--body", "x")
	if code != 0 {
		t.Fatal("comment on legacy ref")
	}
	if ops := e.app().loadOutbox(); len(ops) != 1 || ops[0].Ref != "app#40" {
		t.Fatalf("%+v", ops)
	}
}

func TestLocalmapResolutionAfterPush(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	out, _, _ := e.run("new", "--repo", "app", "--title", "first thing", "--body", "b")
	ref := refOf(t, out)
	eq(t, "ref", ref, "app#L-1")
	out, _, _ = e.run("show", ref)
	contains0(t, "pending show", out, "ref: app#L-1 (local, pending sync: not on GitHub yet)")
	e.gh.handler = func(args []string, stdin string) (string, error) {
		switch {
		case args[0] == "api":
			return "", nil
		case args[0] == "issue" && args[1] == "list":
			return "[]", nil
		case args[0] == "issue" && args[1] == "create":
			return "https://github.com/example/app/issues/612\n", nil
		}
		return "", nil
	}
	if _, _, code := e.run("push"); code != 0 {
		t.Fatal("push")
	}
	eq(t, "localmap format", e.read("localmap.tsv"), "app\tL-1\t612\n")
	// a pull has since mirrored the real issue
	n := fixtureIssueNum(612)
	n.Title = "first thing"
	e.write("app/612.md", renderIssue(n))
	out, _, code := e.run("show", ref)
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	contains0(t, "resolved show", out, "claim: none\nref: L-1 (now #612)\n")
	contains0(t, "resolved show", out, "first thing")
	if strings.Contains(out, "local, pending") {
		t.Fatal("still marked pending")
	}
	// ls lists it under its real number only, no stale local row
	e.write("app/index.tsv", "612\topen\t\t\t2026-02-01T00:00:00Z\tfirst thing\n")
	out, _, _ = e.run("ls", "--repo", "app")
	eq(t, "ls", out, "app#612\topen\t-\t-\tfirst thing\n")
}

func TestLsMarksLocalAndSortsNumerically(t *testing.T) {
	e := newEnv(t)
	seedIndex(e)
	for i := 1; i <= 11; i++ {
		e.run("new", "--repo", "app", "--title", fmt.Sprintf("n%d", i), "--body", "b")
	}
	out, _, _ := e.run("ls", "--repo", "app")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2+11 { // two open rows from the index first, then the local ones
		t.Fatalf("%d lines: %q", len(lines), out)
	}
	if strings.Contains(lines[0], "local") {
		t.Fatalf("mirror row marked local: %q", lines[0])
	}
	for i := 1; i <= 11; i++ {
		l := lines[1+i]
		want := fmt.Sprintf("app#L-%d\topen\t-\t-\tn%d (pending sync)\tlocal", i, i)
		if l != want {
			t.Fatalf("row %d: %q want %q", i, l, want)
		}
	}
	if cmpLocalIdent("L-2", "L-10") >= 0 || cmpLocalIdent("L-20261015194409-e10b", "L-1") >= 0 || cmpLocalIdent("L-9", "L-9") != 0 {
		t.Fatal("cmpLocalIdent")
	}
}

func localEnv(t *testing.T) *env {
	e := newEnv(t)
	os.WriteFile(e.cfg, []byte("mirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n[repos.notes]\nlocal = true\n"), 0o644)
	return e
}

func TestLocalOnlyRepo(t *testing.T) {
	e := localEnv(t)
	e.tick = true
	spawn := filepath.Join(t.TempDir(), "spawn.log")
	e.extra = map[string]string{"ISSUES_TEST_SPAWN": spawn} // autosync enabled: it must skip local-only aliases
	out, _, code := e.run("new", "--repo", "notes", "--title", "buy milk", "--body", "two litres", "--label", "home", "--milestone", "week1")
	eq(t, "new 1", out, "ref notes#1  (#1)\n")
	if code != 0 {
		t.Fatal(code)
	}
	out, _, _ = e.run("new", "--repo", "notes", "--title", "call bank", "--body", "")
	eq(t, "new 2", out, "ref notes#2  (#2)\n")
	if strings.Contains(e.read("notes/1.md"), "local-id") || e.exists("outbox") {
		t.Fatal("local-only issue must not be queued or carry a marker")
	}
	eq(t, "counter", e.read("notes/local-seq"), "2\n")
	if !strings.HasPrefix(e.read("notes/index.tsv"), "2\topen\t") {
		t.Fatal(e.read("notes/index.tsv"))
	}

	out, _, _ = e.run("ls", "--repo", "notes")
	eq(t, "ls", out, "notes#2\topen\t-\t-\tcall bank\nnotes#1\topen\tweek1\thome\tbuy milk\n")
	out, _, _ = e.run("show", "notes#1")
	contains0(t, "show", out, "title: \"buy milk\"")
	contains0(t, "show", out, "two litres")
	if _, err := os.Stat(spawn); err == nil {
		t.Fatal("autosync spawned for a local-only alias")
	}

	if _, _, code = e.run("claim", "notes#1", "--agent", "a1"); code != 0 {
		t.Fatal("claim")
	}
	e.run("comment", "notes#1", "--body", "first comment")
	e.run("comment", "notes#1", "--body", "second comment")
	e.run("label", "notes#1", "--add", "urgent", "--remove", "home")
	e.run("milestone", "notes#2", "week2")
	e.run("edit", "notes#2", "--title", "call the bank", "--body", "before noon")
	out, _, _ = e.run("show", "notes#1")
	contains0(t, "comments", out, "id 1\n\nfirst comment")
	contains0(t, "comments", out, "id 2\n\nsecond comment")
	contains0(t, "label", out, "labels: [\"urgent\"]")
	out, _, _ = e.run("show", "notes#2")
	contains0(t, "edit", out, "title: \"call the bank\"")
	contains0(t, "edit", out, "before noon")
	out, _, _ = e.run("milestones", "--repo", "notes")
	eq(t, "milestones", out, "notes\tweek1\topen\t0\t1\t\nnotes\tweek2\topen\t0\t1\t\n")

	e.run("close", "notes#1", "--reason", "not_planned", "--comment", "dropped", "--agent", "a1")
	out, _, _ = e.run("ls", "--repo", "notes")
	eq(t, "ls after close", out, "notes#2\topen\tweek2\t-\tcall the bank\n")
	out, _, _ = e.run("ls", "--repo", "notes", "--state", "closed")
	eq(t, "ls closed", out, "notes#1\tclosed\tweek1\turgent\tbuy milk\n")
	out, _, _ = e.run("show", "notes#1")
	contains0(t, "closed", out, "state_reason: \"not_planned\"")
	contains0(t, "closed", out, "dropped")
	e.run("reopen", "notes#1")
	out, _, _ = e.run("ls", "--repo", "notes")
	contains0(t, "reopened", out, "notes#1\topen")
	// closing and reopening never frees a number
	out, _, _ = e.run("new", "--repo", "notes", "--title", "third", "--body", "")
	eq(t, "new 3", out, "ref notes#3  (#3)\n")
	_, errs, code := e.run("close", "notes#9")
	if code != 1 || !strings.Contains(errs, "no such issue notes#9") {
		t.Fatalf("%d %q", code, errs)
	}
	out, _, _ = e.run("status")
	contains0(t, "status", out, "notes: local-only, issues=3\n")

	// push, pull and sync are no-ops that exit 0 and never reach GitHub
	for _, cmd := range []string{"push", "pull", "sync"} {
		out, errs, code := e.run(cmd, "--repo", "notes")
		eq(t, cmd, out, "notes is local-only: nothing to sync\n")
		if code != 0 || errs != "" {
			t.Fatalf("%s: %d %q", cmd, code, errs)
		}
	}
	if e.gh.callCount() != 0 {
		t.Fatalf("gh called: %v", e.gh.calls)
	}
	// without --repo, local-only aliases are skipped silently next to GitHub-backed ones
	e.gh.handler = func(args []string, stdin string) (string, error) { return "", &GhError{Msg: "offline"} }
	out, _, _ = e.run("push")
	eq(t, "mixed push", out, "PUSH pushed=0 failed=0\n")
	// the local-only file is never pruned, however old
	e.extra["ISSUES_KEEP_DAYS"] = "0"
	e.now = e.now.AddDate(1, 0, 0)
	e.run("prune")
	if !e.exists("notes/1.md") {
		t.Fatal("pruned a local-only issue")
	}
}

func TestOnlyLocalReposSyncMessage(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(e.cfg, []byte("mirror_dir = \"mirror\"\n[repos.notes]\nlocal = true\n[repos.todo]\nlocal = true\n"), 0o644)
	for _, cmd := range []string{"push", "pull", "sync"} {
		out, _, code := e.run(cmd)
		eq(t, cmd, out, "notes is local-only: nothing to sync\ntodo is local-only: nothing to sync\n")
		if code != 0 {
			t.Fatal(cmd, code)
		}
	}
}

func TestConfigGithubOrLocal(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "moon.toml")
	cases := []struct{ toml, errSub string }{
		{"[repos.x]\ngithub = \"o/x\"\nlocal = true\n", "[repos.x] sets both github and local = true; use exactly one"},
		{"[repos.x]\n", "[repos.x] needs exactly one of github = \"owner/name\" or local = true"},
		{"[repos.x]\nlocal = false\n", "[repos.x] needs exactly one of github"},
		{"[repos.x]\nlocal = \"true\"\n", "local must be true or false (unquoted)"},
	}
	for _, c := range cases {
		os.WriteFile(p, []byte(c.toml), 0o644)
		_, err := LoadConfig(p)
		if err == nil || !strings.Contains(err.Error(), c.errSub) {
			t.Errorf("%q: %v, want %q", c.toml, err, c.errSub)
		}
	}
	os.WriteFile(p, []byte("[repos.n]\nlocal = true # comment\n[repos.g]\ngithub = \"o/g\"\nlocal = false\n"), 0o644)
	c, err := LoadConfig(p)
	if err != nil || len(c.Repos) != 2 || !c.Repos[0].Local || c.Repos[0].GitHub != "" || c.Repos[1].Local || c.Repos[1].GitHub != "o/g" {
		t.Fatalf("%+v %v", c, err)
	}
}
