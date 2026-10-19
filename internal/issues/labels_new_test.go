package issues

import (
	"strings"
	"sync"
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
	var mu sync.Mutex
	var seq []string
	e.gh.handler = func(args []string, stdin string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case args[0] == "api":
			return "", nil
		case args[0] == "issue" && args[1] == "list":
			return "[]", nil
		case args[0] == "label":
			seq = append(seq, strings.Join(args, " "))
		case args[0] == "issue" && args[1] == "create":
			seq = append(seq, "create")
			return "https://github.com/example/app/issues/9\n", nil
		}
		return "", nil
	}
	if out, _, code := e.run("push"); code != 0 || out != "PUSH pushed=2 failed=0\n" {
		t.Fatalf("%d %q", code, out)
	}
	want := "label create area::sim -R example/app --force --color 1d76db --description simulator|create"
	if strings.Join(seq, "|") != want {
		t.Fatalf("%q", strings.Join(seq, "|"))
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
