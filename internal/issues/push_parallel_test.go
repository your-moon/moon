package issues

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPushOverlapsIndependentWrites(t *testing.T) {
	restPush(t)
	e := newEnv(t)
	e.tick = true
	g := e.useMem()
	g.delay = 30 * time.Millisecond
	g.seed("example/app", 10, "2026-01-01T00:00:00Z")
	for i := 1; i <= 10; i++ {
		e.run("close", fmt.Sprintf("app#%d", i))
	}
	out, _, code := e.run("push")
	if code != 0 || out != "PUSH pushed=10 failed=0\n" {
		t.Fatalf("%d %q", code, out)
	}
	if g.maxFlight < 2 || g.maxFlight > pushWorkerLimit {
		t.Fatalf("max in flight %d, want 2..%d", g.maxFlight, pushWorkerLimit)
	}
}

func TestPushKeepsOneIssuesWritesInOrder(t *testing.T) {
	restPush(t)
	e := newEnv(t)
	e.tick = true
	g := e.useMem()
	g.delay = 20 * time.Millisecond
	g.seed("example/app", 3, "2026-01-01T00:00:00Z")
	out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	ref := refOf(t, out)
	e.run("comment", ref, "--body", "c")
	e.run("close", ref)
	for i := 1; i <= 3; i++ {
		e.run("close", fmt.Sprintf("app#%d", i))
	}
	if out, _, code := e.run("push"); code != 0 || out != "PUSH pushed=6 failed=0\n" {
		t.Fatalf("%d %q", code, out)
	}
	// the chain runs create, comment, close (the real number is 101); a write applied before the create
	// landed could not name the issue, so the log shows the order each write was applied in
	chain := []string{"create", "rest-comment 101", "rest-close 101"}
	last := -1
	for _, c := range chain {
		i := -1
		for k, l := range g.log {
			if l == c {
				i = k
			}
		}
		if i <= last {
			t.Fatalf("%q out of order in %v", c, g.log)
		}
		last = i
	}
	if g.maxFlight < 2 {
		t.Fatalf("the independent closes did not overlap with the chain: %v", g.log)
	}
}

func TestPushFailureStaysPerOp(t *testing.T) {
	restPush(t)
	e := newEnv(t)
	e.tick = true
	g := e.useMem()
	g.delay = 5 * time.Millisecond
	g.seed("example/app", 5, "2026-01-01T00:00:00Z")
	g.hook = func(call, repo, ident string) error {
		if call == "CloseIssue" && ident == "2" {
			return &GhError{Msg: "boom"}
		}
		return nil
	}
	for i := 1; i <= 5; i++ {
		e.run("close", fmt.Sprintf("app#%d", i))
	}
	out, _, _ := e.run("push")
	eq(t, "push", out, "PUSH pushed=4 failed=1\n")
	ops := e.app().loadOutbox()
	if len(ops) != 1 || ops[0].Ref != "app#2" || ops[0].LastError == nil || !strings.Contains(*ops[0].LastError, "boom") || ops[0].Attempts != 1 {
		t.Fatalf("%+v", ops)
	}
	if i := g.issue("example/app", 3); i.state != "CLOSED" {
		t.Fatalf("the other closes must still land: %+v", i)
	}
}

func TestNextBatchOrder(t *testing.T) {
	mk := func(id, op, ref string, args map[string]any) *Op {
		return &Op{OpID: id, Op: op, Ref: ref, Args: args}
	}
	ids := func(b []*Op) string {
		var s []string
		for _, o := range b {
			s = append(s, o.OpID)
		}
		return strings.Join(s, ",")
	}
	none := map[string]bool{}
	all := []*Op{
		mk("a", "new", "app#L-1", map[string]any{}),
		mk("b", "close", "app#L-1", nil),
		mk("c", "close", "app#7", nil),
		mk("d", "label", "app#7", nil),
		mk("e", "close", "lib#1", nil),
	}
	eq(t, "one per issue", ids(nextBatch(all, none, none, none, "", "", 4)), "a,c,e")
	eq(t, "limit", ids(nextBatch(all, none, none, none, "", "", 2)), "a,c")
	eq(t, "tried skipped, next of the issue goes", ids(nextBatch(all, none, map[string]bool{"a": true}, none, "", "", 4)), "b,c,e")
	eq(t, "bad ref blocks the issue", ids(nextBatch(all, map[string]bool{"app#7": true}, none, none, "", "", 4)), "a,e")
	eq(t, "repo filter", ids(nextBatch(all, none, none, none, "lib", "", 4)), "e")

	// a milestone before the issues that use it; the issue's later writes wait behind it
	ms := []*Op{
		mk("m", "ms_new", "app#M-1", map[string]any{"title": "v9"}),
		mk("n", "new", "app#L-2", map[string]any{"milestone": "v9"}),
		mk("o", "close", "app#L-2", nil),
		mk("p", "close", "app#9", nil),
		mk("q", "ms_edit", "app#M-1", map[string]any{"title": "v9", "due": "2026-01-01"}),
	}
	eq(t, "milestone first", ids(nextBatch(ms, none, none, none, "", "", 4)), "m,p")
	eq(t, "after the milestone landed", ids(nextBatch(ms[1:], none, none, none, "", "", 4)), "n,p,q")
}

// TestPushPoolSizes prints how long a 17-write queue (the size moon#17 measured) takes at each pool size with a
// fixed 100 ms per gh call. Run: MOON_POOL_BENCH=1 go test ./internal/issues -run PoolSizes -v
func TestPushPoolSizes(t *testing.T) {
	if os.Getenv("MOON_POOL_BENCH") == "" {
		t.Skip("set MOON_POOL_BENCH=1")
	}
	for _, n := range []int{1, 2, 4, 8, 16} {
		e := newEnv(t)
		e.tick = true
		g := e.useMem()
		g.delay = 100 * time.Millisecond
		g.seed("example/app", 12, "2026-01-01T00:00:00Z")
		old := pushWorkers
		pushWorkers = n
		for i := 1; i <= 12; i++ {
			e.run("close", fmt.Sprintf("app#%d", i))
		}
		out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
		ref := refOf(t, out)
		e.run("comment", ref, "--body", "c")
		e.run("label", ref, "--add", "x")
		e.run("close", ref)
		e.run("label", "app#1", "--add", "y") // behind app#1's close
		start := time.Now()
		e.run("push")
		t.Logf("workers=%-2d 17 writes: %v", n, time.Since(start).Round(10*time.Millisecond))
		pushWorkers = old
	}
}
