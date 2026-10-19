package issues

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// seedPull seeds n issues updated on 2026-02-01..20, for the delta pull tests.
func seedPull(f *memGH, n int) {
	f.seed("example/app", n, "")
	for i := 1; i <= n; i++ {
		f.issue("example/app", i).updated = fmt.Sprintf("2026-02-%02dT00:00:00Z", 1+i%20)
	}
}

func TestDeltaPullTouchesOnlyChanged(t *testing.T) {
	e := newEnv(t)
	f := e.useMem()
	seedPull(f, 50)
	if out, _, code := e.run("pull", "--repo", "app"); code != 0 || out != "PULL app=50\n" {
		t.Fatalf("first pull: %q %d", out, code)
	}
	if f.callCount() != 1 {
		t.Fatalf("first pull made %d gh calls, want 1 (issues, labels, milestones and PRs together)", f.callCount())
	}
	// an unchanged file edited on disk: a delta must leave it alone, a full pull restores it
	e.putIssue("app", "7", e.getIssue("app", "7")+"LOCAL\n")
	f.issue("example/app", 3).updated, f.issue("example/app", 4).updated = "2026-03-01T00:00:00Z", "2026-03-01T00:00:00Z"
	e.now = e.now.Add(time.Hour)
	before := f.callCount()
	out, _, code := e.run("pull", "--repo", "app")
	eq(t, "delta pull", out, "PULL app=2\n")
	if code != 0 || f.callCount()-before != 1 {
		t.Fatalf("delta pull: code %d, %d gh calls, want 1", code, f.callCount()-before)
	}
	if f.sinces[len(f.sinces)-1] == "" {
		t.Fatal("delta pull sent no since")
	}
	if !strings.HasSuffix(e.getIssue("app", "7"), "LOCAL\n") {
		t.Fatal("delta pull rewrote an unchanged issue file")
	}
	idx := e.getFile("app", fileIndex)
	if !strings.Contains(idx, "\n4\topen\t\t\t2026-03-01T00:00:00Z\tt4\n") && !strings.HasPrefix(idx, "4\t") {
		t.Fatalf("index row of a changed issue not updated:\n%s", idx)
	}
	if len(strings.Split(strings.TrimSpace(idx), "\n")) != 50 {
		t.Fatal("index lost rows on a delta pull")
	}
	// --full re-reads everything and drops what GitHub no longer has
	delete(f.repo("example/app").issues, 9)
	out, _, _ = e.run("pull", "--repo", "app", "--full")
	if f.sinces[len(f.sinces)-1] != "" || e.hasIssue("app", "9") {
		t.Fatalf("--full did not fetch everything: %q since=%q", out, f.sinces[len(f.sinces)-1])
	}
	if strings.HasSuffix(e.getIssue("app", "7"), "LOCAL\n") {
		t.Fatal("--full left the tampered file")
	}
}

func TestPullWithoutLastpullIsFull(t *testing.T) {
	e := newEnv(t)
	f := e.useMem()
	seedPull(f, 5)
	e.run("pull", "--repo", "app")
	e.rawRemove("app/lastpull")
	e.run("pull", "--repo", "app")
	if got := f.sinces[len(f.sinces)-1]; got != "" {
		t.Fatalf("pull without lastpull sent since=%q, want a full pull", got)
	}
}

// TestMeasurePull prints gh calls and time for a 50-issue pull with 2 changed (MOON_MEASURE=1).
func TestMeasurePull(t *testing.T) {
	if os.Getenv("MOON_MEASURE") == "" {
		t.Skip("set MOON_MEASURE=1")
	}
	e := newEnv(t)
	f := e.useMem()
	seedPull(f, 50)
	f.delay = 100 * time.Millisecond
	e.run("pull", "--repo", "app")
	f.issue("example/app", 3).updated, f.issue("example/app", 4).updated = "2026-03-01T00:00:00Z", "2026-03-01T00:00:00Z"
	e.now = e.now.Add(time.Hour)
	c0, t0 := f.callCount(), time.Now()
	e.run("pull", "--repo", "app")
	t.Logf("MEASURE pull 50 issues, 2 changed: %d gh calls, %v", f.callCount()-c0, time.Since(t0))
}
