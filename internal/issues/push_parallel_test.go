package issues

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// latencyGH makes every gh call take d, and records how many ran at once and the start/end order per issue.
type latencyGH struct {
	mu        sync.Mutex
	inflight  int
	maxFlight int
	log       []string
}

func (l *latencyGH) handler(d time.Duration, fail map[string]bool) func([]string, string) (string, error) {
	return func(args []string, stdin string) (string, error) {
		key := ""
		switch {
		case args[0] == "api":
			return "", nil
		case args[0] == "issue" && args[1] == "list":
			return "[]", nil
		case args[0] == "issue" && args[1] == "create":
			key = "create"
		default:
			key = args[1] + " " + args[2]
		}
		l.mu.Lock()
		l.inflight++
		l.maxFlight = max(l.maxFlight, l.inflight)
		l.log = append(l.log, "start "+key)
		l.mu.Unlock()
		time.Sleep(d)
		l.mu.Lock()
		l.inflight--
		l.log = append(l.log, "end "+key)
		l.mu.Unlock()
		if fail[key] {
			return "", &GhError{Msg: "boom " + key}
		}
		if key == "create" {
			return "https://github.com/example/app/issues/50\n", nil
		}
		return "", nil
	}
}

func (l *latencyGH) index(s string) int {
	for i, x := range l.log {
		if x == s {
			return i
		}
	}
	return -1
}

func TestPushOverlapsIndependentWrites(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.gh.parallel = true
	lat := &latencyGH{}
	e.gh.handler = lat.handler(30*time.Millisecond, nil)
	for i := 1; i <= 10; i++ {
		e.run("close", fmt.Sprintf("app#%d", i))
	}
	out, _, code := e.run("push")
	if code != 0 || out != "PUSH pushed=10 failed=0\n" {
		t.Fatalf("%d %q", code, out)
	}
	if lat.maxFlight < 2 || lat.maxFlight > pushWorkerLimit {
		t.Fatalf("max in flight %d, want 2..%d", lat.maxFlight, pushWorkerLimit)
	}
}

func TestPushKeepsOneIssuesWritesInOrder(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.gh.parallel = true
	lat := &latencyGH{}
	e.gh.handler = lat.handler(20*time.Millisecond, nil)
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
	// the chain runs create, comment, close (the real number is 50), each after the one before ended
	chain := []string{"start create", "end create", "start comment 50", "end comment 50", "start close 50", "end close 50"}
	last := -1
	for _, c := range chain {
		i := lat.index(c)
		if i <= last {
			t.Fatalf("%q out of order in %v", c, lat.log)
		}
		last = i
	}
	if lat.maxFlight < 2 {
		t.Fatalf("the independent closes did not overlap with the chain: %v", lat.log)
	}
}

func TestPushFailureStaysPerOp(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.gh.parallel = true
	lat := &latencyGH{}
	e.gh.handler = func(args []string, stdin string) (string, error) {
		if args[0] == "issue" && args[1] == "close" && args[2] == "2" {
			return "", &GhError{Msg: "boom"}
		}
		return lat.handler(5*time.Millisecond, nil)(args, stdin)
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
		e.gh.parallel = true
		e.gh.handler = (&latencyGH{}).handler(100*time.Millisecond, nil)
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
