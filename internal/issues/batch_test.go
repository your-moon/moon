package issues

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPushBatchesIndependentWrites(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := e.useMem()
	f.seed("example/app", 14, "2026-01-01T00:00:00Z")
	f.addLabels("example/app", "bug", "old")
	f.addMilestone("example/app", "v1")
	for i := 1; i <= 5; i++ {
		e.run("close", fmt.Sprintf("app#%d", i), "--comment", "done")
	}
	for i := 6; i <= 9; i++ {
		e.run("comment", fmt.Sprintf("app#%d", i), "--body", "hello")
	}
	e.run("label", "app#10", "--add", "bug", "--remove", "old")
	e.run("edit", "app#11", "--title", "new title", "--body", "new body")
	e.run("reopen", "app#12")
	e.run("milestone", "app#13", "v1")
	e.run("milestone", "app#14", "none")
	out, _, code := e.run("push")
	if code != 0 || out != "PUSH pushed=14 failed=0\n" {
		t.Fatalf("%d %q", code, out)
	}
	if f.callCount() != 2 || f.count("ResolveIDs") != 1 || f.count("Mutate") != 1 {
		t.Fatalf("GitHub calls %v; want 1 resolve + 1 mutate, no REST", f.calls)
	}
	got := strings.Join(f.log, ",")
	for _, want := range []string{"closeIssue ID1", "addComment ID5", "addComment ID6", "addLabelsToLabelable ID10", "removeLabelsFromLabelable ID10",
		"updateIssue ID11", "reopenIssue ID12", "updateIssue ID13", "updateIssue ID14"} {
		if !strings.Contains(got, want) {
			t.Fatalf("mutation %q missing from %s", want, got)
		}
	}
	if left := e.app().loadOutbox(); len(left) != 0 {
		t.Fatalf("%d ops left queued", len(left))
	}
}

func TestPushBatchBatches20AtATime(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := e.useMem()
	f.seed("example/app", 45, "2026-01-01T00:00:00Z")
	for i := 1; i <= 45; i++ {
		e.run("reopen", fmt.Sprintf("app#%d", i))
	}
	out, _, _ := e.run("push")
	if out != "PUSH pushed=45 failed=0\n" {
		t.Fatalf("%q", out)
	}
	if f.muts != 45 || f.callCount() > 6 { // 3 mutation requests of at most 20, each with at most one resolve
		t.Fatalf("%d mutations in %d requests", f.muts, f.callCount())
	}
}

// A write that fails stays queued with its error, the rest of the batch lands, and the failed issue's next write
// is not sent in the same push.
func TestPushBatchFailureIsPerOpAndKeepsIssueOrder(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := e.useMem()
	f.seed("example/app", 4, "2026-01-01T00:00:00Z")
	f.fail["ID2"] = "Something went wrong while executing your query"
	e.run("edit", "app#2", "--title", "first")
	for i := 1; i <= 4; i++ {
		if i != 2 {
			e.run("reopen", fmt.Sprintf("app#%d", i))
		}
	}
	e.run("close", "app#2")
	out, _, _ := e.run("push")
	eq(t, "push", out, "PUSH pushed=3 failed=1\n")
	for _, l := range f.log {
		if l == "closeIssue ID2" {
			t.Fatalf("the close ran although the edit before it failed: %v", f.log)
		}
	}
	left := e.app().loadOutbox()
	if len(left) != 2 || left[0].Op != "edit" || left[0].LastError == nil || !strings.Contains(*left[0].LastError, "Something went wrong") || left[1].Op != "close" || left[1].Attempts != 0 {
		t.Fatalf("queue after a failed edit: %d ops %+v", len(left), left)
	}
}

func TestPushBatchPermanentErrorStopsRetrying(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := e.useMem()
	f.seed("example/app", 3, "2026-01-01T00:00:00Z")
	f.fail["ID1"] = "Could not resolve to a Repository with the name 'example/app'."
	for i := 1; i <= 3; i++ {
		e.run("reopen", fmt.Sprintf("app#%d", i))
	}
	e.run("push")
	left := e.app().loadOutbox()
	if len(left) != 1 || !left[0].Permanent {
		t.Fatalf("want the one failed op marked permanent, got %+v", left)
	}
}

// A new issue is created over REST; the writes queued on it follow in a later round with its real number.
func TestPushBatchNewBeforeItsEdits(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	f := e.useMem()
	f.seed("example/app", 3, "2026-01-01T00:00:00Z")
	f.addLabels("example/app", "x")
	out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
	ref := refOf(t, out)
	e.run("comment", ref, "--body", "c")
	e.run("label", ref, "--add", "x")
	for i := 1; i <= 3; i++ {
		e.run("reopen", fmt.Sprintf("app#%d", i))
	}
	out, _, _ = e.run("push")
	eq(t, "push", out, "PUSH pushed=6 failed=0\n")
	got := strings.Join(f.log, ",")
	ci, co, ce := strings.Index(got, "create"), strings.Index(got, "rest-comment 101"), strings.Index(got, "rest-edit 101")
	if ci < 0 || co < ci || ce < co || strings.Count(got, "reopenIssue") != 3 {
		t.Fatalf("order of effects: %s", got)
	}
}

// MeasurePush: 17 writes, 100 ms per gh call. MOON_MEASURE=1 go test ./internal/issues -run MeasurePush -v
func TestMeasurePush(t *testing.T) {
	if os.Getenv("MOON_MEASURE") == "" {
		t.Skip("set MOON_MEASURE=1")
	}
	for _, mode := range []string{"rest", "batch", "rest-independent", "batch-independent"} {
		e := newEnv(t)
		e.tick = true
		f := e.useMem()
		f.delay = 100 * time.Millisecond
		f.seed("example/app", 17, "2026-01-01T00:00:00Z")
		f.addLabels("example/app", "x", "y")
		if strings.HasPrefix(mode, "rest") {
			restPush(t)
		} else {
			pushBatch = 20
		}
		if strings.HasSuffix(mode, "independent") { // 17 writes on 17 issues
			for i := 1; i <= 17; i++ {
				e.run("comment", fmt.Sprintf("app#%d", i), "--body", "c")
			}
		} else { // 12 closes, a new issue with 3 writes behind it, a label behind a close
			for i := 1; i <= 12; i++ {
				e.run("close", fmt.Sprintf("app#%d", i))
			}
			out, _, _ := e.run("new", "--repo", "app", "--title", "t", "--body", "b")
			ref := refOf(t, out)
			e.run("comment", ref, "--body", "c")
			e.run("label", ref, "--add", "x")
			e.run("close", ref)
			e.run("label", "app#1", "--add", "y")
		}
		calls := f.callCount
		c0, t0 := calls(), time.Now()
		e.run("push")
		t.Logf("MEASURE push 17 writes (%s): %d gh calls, %v", mode, calls()-c0, time.Since(t0).Round(10*time.Millisecond))
	}
}
