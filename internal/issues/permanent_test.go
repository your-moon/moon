package issues

import (
	"os"
	"strings"
	"testing"
)

func TestPermanentErrorIsNotRetried(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.run("close", "app#1")
	e.run("close", "app#2")
	e.run("close", "lib#3")
	g := e.useMem()
	g.seed("example/app", 2, "2026-01-01T00:00:00Z")
	g.seed("example/lib", 3, "2026-01-01T00:00:00Z")
	disabled := true
	g.hook = func(call, repo, ident string) error {
		if call == "CloseIssue" && repo == "example/app" && disabled {
			return &GhError{Msg: "the 'example/app' repository has disabled issues"}
		}
		return nil
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
	before := g.callCount()
	out, _, _ = e.run("push")
	eq(t, "second push", out, "PUSH pushed=0 failed=0\n")
	if g.callCount() != before {
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
	g := e.useMem()
	g.seed("example/app", 1, "2026-01-01T00:00:00Z")
	g.hook = func(call, repo, ident string) error { return &GhError{Msg: "HTTP 502"} }
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
	g := e.useMem()
	e.run("sync", "--all", "--quiet")
	e.run("push", "--all")
	for _, c := range g.calls { // only the pull of app may reach GitHub
		if !strings.HasPrefix(c, "Pull") {
			t.Fatalf("GitHub was called for a local-only repo: %v", g.calls)
		}
	}
	if len(e.app().loadOutbox()) != 1 {
		t.Fatal("the queued write must stay")
	}
}

func TestAutosyncSwitchesAndStatus(t *testing.T) {
	e := newEnv(t)
	status := func() string { o, _, _ := e.run("status"); return o }
	eq(t, "legacy env", strings.Split(status(), "\n")[1], "autosync: off (ISSUES_NO_AUTOSYNC)")
	delete(e.extra, "ISSUES_NO_AUTOSYNC")
	eq(t, "on, never ran", strings.Split(status(), "\n")[1], "autosync: on, has not run yet")
	e.extra["MOON_AUTOSYNC"] = "0"
	eq(t, "env off", strings.Split(status(), "\n")[1], "autosync: off (MOON_AUTOSYNC=0)")
	delete(e.extra, "MOON_AUTOSYNC")
	e.write("../moon.toml", "autosync = false\nmirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n")
	eq(t, "config off", strings.Split(status(), "\n")[1], "autosync: off (autosync = false in moon.toml)")
	e.extra["MOON_AUTOSYNC"] = "1"
	eq(t, "env on beats config", strings.Split(status(), "\n")[1], "autosync: on, has not run yet")
	e.write("autosync.log", "2026-10-19T10:00:00 SYNC pushed=0 failed=5 pulled app=0 in 21.5s\n")
	eq(t, "last run", strings.Split(status(), "\n")[1], "autosync: on, last run 2026-10-19T10:00:00 SYNC pushed=0 failed=5 pulled app=0 in 21.5s")
	if o, _, _ := e.run("status", "--format", "json"); !strings.Contains(o, `"autosync": {"on": true, "off_reason": null, "last_run": "2026-10-19T10:00:00 SYNC`) {
		t.Fatal(o)
	}
}

func TestAutosyncOffStopsTheBackgroundSpawn(t *testing.T) {
	for _, off := range []map[string]string{{"MOON_AUTOSYNC": "0"}, {"ISSUES_NO_AUTOSYNC": "1"}} {
		e := newEnv(t)
		delete(e.extra, "ISSUES_NO_AUTOSYNC")
		for k, v := range off {
			e.extra[k] = v
		}
		spawn := e.home + "-spawn"
		e.extra["ISSUES_TEST_SPAWN"] = spawn
		seedIndex(e) // no lastpull: the mirror counts as never pulled
		e.run("ls", "--repo", "app")
		if _, err := os.Stat(spawn); err == nil {
			t.Fatalf("%v: a read started a background sync", off)
		}
	}
	e := newEnv(t)
	delete(e.extra, "ISSUES_NO_AUTOSYNC")
	e.extra["ISSUES_TEST_SPAWN"] = e.home + "-spawn"
	seedIndex(e)
	e.run("ls", "--repo", "app")
	if _, err := os.Stat(e.extra["ISSUES_TEST_SPAWN"]); err != nil {
		t.Fatal("control: with autosync on the read must spawn")
	}
}

func TestAutosyncOffStopsTheBackgroundPushAfterAWrite(t *testing.T) {
	for _, off := range []map[string]string{{"MOON_AUTOSYNC": "0"}, {"ISSUES_NO_AUTOSYNC": "1"}} {
		e := newEnv(t)
		delete(e.extra, "ISSUES_NO_AUTOSYNC")
		for k, v := range off {
			e.extra[k] = v
		}
		spawn := e.home + "-spawn"
		e.extra["ISSUES_TEST_SPAWN"] = spawn
		seedIndex(e)
		e.run("comment", "app#9", "--body", "hi")
		if _, err := os.Stat(spawn); err == nil {
			t.Fatalf("%v: a write started a background push", off)
		}
		if len(e.app().loadOutbox()) != 1 {
			t.Fatalf("%v: the write must stay queued", off)
		}
	}
	e := newEnv(t) // moon.toml says autosync = false
	delete(e.extra, "ISSUES_NO_AUTOSYNC")
	e.write("../moon.toml", "autosync = false\nmirror_dir = \"mirror\"\n[repos.app]\ngithub = \"example/app\"\n")
	e.extra["ISSUES_TEST_SPAWN"] = e.home + "-spawn"
	seedIndex(e)
	e.run("comment", "app#9", "--body", "hi")
	if _, err := os.Stat(e.extra["ISSUES_TEST_SPAWN"]); err == nil {
		t.Fatal("config off: a write started a background push")
	}
}

func TestPermanentOpDoesNotKickABackgroundPush(t *testing.T) {
	e := newEnv(t)
	e.run("close", "app#1")
	g := e.useMem()
	g.seed("example/app", 1, "2026-01-01T00:00:00Z")
	g.hook = func(call, repo, ident string) error {
		return &GhError{Msg: "the 'example/app' repository has disabled issues"}
	}
	e.run("push")
	if ops := e.app().loadOutbox(); len(ops) != 1 || !ops[0].Permanent {
		t.Fatalf("setup: %+v", ops)
	}
	delete(e.extra, "ISSUES_NO_AUTOSYNC")
	spawn := e.home + "-spawn"
	e.extra["ISSUES_TEST_SPAWN"] = spawn
	a := e.app()
	if a.remoteOpsQueued() {
		t.Fatal("a permanently failed op counts as sendable")
	}
	a.AfterWrite()
	if _, err := os.Stat(spawn); err == nil {
		t.Fatal("AfterWrite spawned a sync that can send nothing")
	}
}
