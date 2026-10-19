package issues

import (
	"strings"
	"testing"
)

func TestPermanentErrorIsNotRetried(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.run("close", "app#1")
	e.run("close", "app#2")
	e.run("close", "lib#3")
	disabled := true
	e.gh.handler = func(args []string, stdin string) (string, error) {
		if args[0] == "issue" && args[4] == "example/app" && disabled {
			return "", &GhError{Msg: "the 'example/app' repository has disabled issues"}
		}
		return "", nil
	}
	out, _, _ := e.run("push")
	eq(t, "first push", out, "PUSH pushed=1 failed=2\n")
	ops := e.app().loadOutbox()
	if len(ops) != 2 {
		t.Fatalf("%d ops left", len(ops))
	}
	for _, op := range ops {
		if !op.Permanent || op.LastError == nil || !strings.HasPrefix(*op.LastError, permanentPrefix) {
			t.Fatalf("%+v", op)
		}
	}
	// the second push neither tries them nor counts them
	before := e.gh.callCount()
	out, _, _ = e.run("push")
	eq(t, "second push", out, "PUSH pushed=0 failed=0\n")
	if e.gh.callCount() != before {
		t.Fatal("a permanent op was sent again")
	}
	if o, _, _ := e.run("outbox", "ls"); !strings.Contains(o, "permanent, no retry: the 'example/app' repository has disabled issues") {
		t.Fatalf("outbox ls hides why:\n%s", o)
	}
	// retry clears it; once GitHub accepts, it goes
	disabled = false
	if _, errs, code := e.run("outbox", "retry", ops[0].OpID); code != 0 {
		t.Fatal(errs)
	}
	out, _, _ = e.run("push")
	eq(t, "after retry", out, "PUSH pushed=1 failed=0\n")
	if got := e.app().loadOutbox(); len(got) != 1 || got[0].OpID != ops[1].OpID {
		t.Fatalf("%+v", got)
	}
}

func TestTemporaryErrorIsRetried(t *testing.T) {
	e := newEnv(t)
	e.run("close", "app#1")
	e.gh.handler = func(args []string, stdin string) (string, error) { return "", &GhError{Msg: "HTTP 502"} }
	e.run("push")
	out, _, _ := e.run("push")
	eq(t, "retried", out, "PUSH pushed=0 failed=1\n")
	if op := e.app().loadOutbox()[0]; op.Permanent || op.Attempts != 2 {
		t.Fatalf("%+v", op)
	}
}

func TestSyncSkipsLocalOnlyRepoWithQueuedOps(t *testing.T) {
	e := newEnv(t)
	e.run("close", "lib#3")
	e.write("../moon.toml", "mirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n[repos.lib]\nlocal = true\n")
	e.gh.handler = func(args []string, stdin string) (string, error) { return "", nil }
	e.run("sync", "--all", "--quiet")
	e.run("push", "--all")
	if n := e.gh.count("issue"); n != 0 { // the pull of app is a graphql call
		t.Fatalf("gh was called %d times for a local-only repo: %v", n, e.gh.calls)
	}
	if len(e.app().loadOutbox()) != 1 {
		t.Fatal("the queued write must stay")
	}
}
