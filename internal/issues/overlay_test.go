package issues

import (
	"strings"
	"testing"
)

func seedOverlay(e *env) {
	e.write("app/index.tsv", "4\topen\t\tblocked::depends\t2026-02-01T00:00:00Z\tfourth\n3\topen\tv1\tbug\t2026-02-01T00:00:00Z\tthird\n2\topen\t\t\t2026-02-01T00:00:00Z\tsecond\n1\tclosed\t\t\t2026-02-01T00:00:00Z\tfirst\n")
}

func lsRow(t *testing.T, e *env, ref string) string {
	t.Helper()
	out, _, _ := e.run("ls", "--repo", "app", "--state", "all")
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, ref+"\t") {
			return l
		}
	}
	t.Fatalf("%s not in ls:\n%s", ref, out)
	return ""
}

func TestReadsOverlayQueuedOps(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	seedOverlay(e)
	e.run("close", "app#2")
	e.run("reopen", "app#1")
	e.run("label", "app#3", "--add", "x", "--remove", "bug")
	e.run("milestone", "app#2", "v2")
	e.run("edit", "app#4", "--title", "fourth renamed")
	eq(t, "close", lsRow(t, e, "app#2"), "app#2\tclosed\tv2\t-\tsecond")
	eq(t, "reopen", lsRow(t, e, "app#1"), "app#1\topen\t-\t-\tfirst")
	eq(t, "label", lsRow(t, e, "app#3"), "app#3\topen\tv1\tx\tthird")
	eq(t, "edit", lsRow(t, e, "app#4"), "app#4\topen\t-\tblocked::depends\tfourth renamed")
	// milestone none clears it
	e.run("milestone", "app#3", "none")
	eq(t, "milestone none", lsRow(t, e, "app#3"), "app#3\topen\t-\tx\tthird")
	// the mirror itself is untouched
	if !strings.Contains(e.read("app/index.tsv"), "2\topen\t\t\t") {
		t.Fatal(e.read("app/index.tsv"))
	}
}

func TestFrontierFollowsQueuedCloseAndUnblock(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	seedOverlay(e)
	refs := func() string {
		out, _, _ := e.run("frontier", "--repo", "app", "-i")
		var r []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" {
				r = append(r, strings.Split(l, "\t")[0])
			}
		}
		return strings.Join(r, " ")
	}
	eq(t, "before", refs(), "app#3 app#2")
	e.run("close", "app#2")
	e.run("label", "app#4", "--remove", "blocked::depends")
	eq(t, "after", refs(), "app#4 app#3")
}

func TestOverlayStopsWhenOpFailsOrIsDropped(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	seedOverlay(e)
	e.run("close", "app#2")
	eq(t, "queued", lsRow(t, e, "app#2"), "app#2\tclosed\t-\t-\tsecond")
	ops := e.app().loadOutbox()
	msg := "boom"
	ops[0].LastError = &msg
	if err := e.app().saveOp(ops[0]); err != nil {
		t.Fatal(err)
	}
	eq(t, "failed", lsRow(t, e, "app#2"), "app#2\topen\t-\t-\tsecond")
	ops[0].LastError = nil
	e.app().saveOp(ops[0])
	eq(t, "retry ok", lsRow(t, e, "app#2"), "app#2\tclosed\t-\t-\tsecond")
	e.run("outbox", "drop", ops[0].OpID)
	eq(t, "dropped", lsRow(t, e, "app#2"), "app#2\topen\t-\t-\tsecond")
}

func TestPullIndexIgnoresQueuedOps(t *testing.T) {
	e := newEnv(t)
	seedOverlay(e)
	e.run("close", "app#2")
	rows := e.app().readIndexRaw("app")
	for _, r := range rows {
		if r[0] == "2" && r[1] != "open" {
			t.Fatalf("raw index carries the overlay: %v", r)
		}
	}
}
