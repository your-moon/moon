package issues

import (
	"strings"
	"testing"
)

func TestLabelsNewQueuesAndUnlocksOfflineUse(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.write("app/labels.tsv", "bug\t d73a4a\n")
	_, errs, code := e.run("new", "--repo", "app", "--title", "t", "--body", "b", "-L", "area::sim")
	if code == 0 || !strings.Contains(errs, "labels new") {
		t.Fatalf("unknown label must be refused and name the command: %d %q", code, errs)
	}
	if out, errs, code := e.run("labels", "new", "area::sim", "--color", "#1d76db", "-R", "app"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, errs, code := e.run("labels", "new", "AREA::sim", "-R", "app"); code == 0 || !strings.Contains(errs, "already") {
		t.Fatalf("duplicate create: %d %q", code, errs)
	}
	if _, _, code := e.run("labels", "new", "x", "--color", "nothex", "-R", "app"); code != 2 {
		t.Fatalf("bad colour: %d", code)
	}
	out, errs, code := e.run("new", "--repo", "app", "--title", "t", "--body", "b", "-L", "area::sim")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	ref := refOf(t, out)
	if _, _, code := e.run("label", ref, "--add", "area::SIM"); code != 0 {
		t.Fatal("label --add with a queued label")
	}
	if o, _, _ := e.run("outbox", "ls"); !strings.Contains(o, "create label area::sim") {
		t.Fatal(o)
	}
}

func TestPushCreatesLabelBeforeItsUsers(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.write("app/labels.tsv", "bug\t d73a4a\n")
	e.run("labels", "new", "area::sim", "--color", "1d76db", "-D", "simulator", "-R", "app")
	e.run("new", "--repo", "app", "--title", "t", "--body", "b", "-L", "area::sim")
	g := e.useMem()
	if out, _, code := e.run("push"); code != 0 || out != "PUSH pushed=2 failed=0\n" {
		t.Fatalf("%d %q", code, out)
	}
	eq(t, "label before its user", strings.Join(g.log, ","), "create-label area::sim,create")
	if len(g.labelSpecs) != 1 || g.labelSpecs[0] != (LabelSpec{Name: "area::sim", Color: "1d76db", Description: "simulator"}) {
		t.Fatalf("label: %+v", g.labelSpecs)
	}
	if len(g.created) != 1 || strings.Join(g.created[0].Labels, ",") != "area::sim" {
		t.Fatalf("create: %+v", g.created)
	}
}

func TestLabelWaitingOrder(t *testing.T) {
	ops := []*Op{
		{OpID: "n", Op: "new", Ref: "app#L-1", Args: map[string]any{"labels": []any{"x"}}},
		{OpID: "l", Op: "label_new", Ref: "app#label:X", Args: map[string]any{"name": "X"}},
		{OpID: "o", Op: "new", Ref: "lib#L-1", Args: map[string]any{"labels": []any{"x"}}},
	}
	if !labelWaiting(ops, 0) || labelWaiting(ops, 1) || labelWaiting(ops, 2) {
		t.Fatal("only the issue in the same repo waits")
	}
}
