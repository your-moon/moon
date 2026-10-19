package issues

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sessionEnv(t *testing.T) (*env, *Session) {
	e := newEnv(t)
	e.putFile("app", fileIndex, "3\topen\tv1\tbug,area::ui\t2026-02-03T00:00:00Z\tThird thing\n2\topen\t\t\t2026-02-02T00:00:00Z\tsecond: ünï\n1\tclosed\t\tdocs\t2026-02-01T00:00:00Z\tfirst\n")
	e.putFile("app", fileMilestones, "v1\topen\t2\t1\t2026-04-01T00:00:00Z\nv2\topen\t0\t0\t\n")
	s, err := NewSession(e.cfg, "", Env{Getenv: func(k string) string { return e.extra[k] }, Cwd: "/", Now: func() time.Time { return e.now }})
	if err != nil {
		t.Fatal(err)
	}
	return e, s
}

func TestSessionLoadAndWrites(t *testing.T) {
	e, s := sessionEnv(t)
	snap := s.Load()
	if snap.Missing || len(snap.Items) != 3 || snap.Items[0].Milestone != "v1" || snap.PullAgeSec != -1 {
		t.Fatalf("%+v", snap)
	}
	if rows := s.Milestones(); len(rows) != 2 || rows[0].Closed != 2 || rows[0].Open != 1 || rows[0].Due == "" {
		t.Fatalf("%+v", rows)
	}
	if _, err := s.Do("comment", "app#3", "me", []string{"hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Do("milestone", "app#3", "me", []string{"v2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Do("close", "app#2", "me", nil); err != nil {
		t.Fatal(err)
	}
	// the CLI sees exactly the same three ops
	out, _, _ := e.run("status")
	contains0(t, "status", out, "outbox=3")
	snap = s.Load()
	if snap.Queued != 3 || snap.Items[0].Milestone != "v2" || snap.Items[1].State != "closed" {
		t.Fatalf("%+v", snap)
	}
}

func TestSessionClaimNeverForces(t *testing.T) {
	e, s := sessionEnv(t)
	if _, err := s.Do("claim", "app#3", "robin", nil); err != nil {
		t.Fatal(err)
	}
	if msg, err := s.Do("claim", "app#3", "me", nil); err != ErrHeld || !strings.Contains(msg, "robin") {
		t.Fatalf("%q %v", msg, err)
	}
	if _, err := s.Do("release", "app#3", "me", nil); err != ErrHeld {
		t.Fatalf("release by another: %v", err)
	}
	out, _, _ := e.run("claims")
	contains0(t, "claims", out, "app#3")
	if _, err := s.Do("release", "app#3", "robin", nil); err != nil {
		t.Fatal(err)
	}
	if len(s.Claims()) != 0 {
		t.Fatal("claim not released")
	}
}

func TestQueryMatchEqualsLsQ(t *testing.T) {
	e, s := sessionEnv(t)
	for _, q := range []string{"third", "BUG", "area::", "ünï", "docs", "zzz", "bug,area", "x"} {
		out, _, _ := e.run("ls", "--state", "all", "-q", q)
		for _, it := range s.Load().Items {
			want := strings.Contains(out, it.Ref+"\t")
			if got := QueryMatch(it.Title, strings.Join(it.Labels, ","), q); got != want {
				t.Errorf("q=%q %s: QueryMatch=%v ls=%v", q, it.Ref, got, want)
			}
		}
	}
}

func TestSessionMissingMirror(t *testing.T) {
	e := newEnv(t)
	s, err := NewSession(e.cfg, "", Env{Getenv: func(string) string { return "" }, Cwd: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if snap := s.Load(); !snap.Missing || len(snap.Items) != 0 {
		t.Fatalf("%+v", snap)
	}
	if _, err := s.Detail("app#9"); err == nil {
		t.Fatal("want an error, not a panic")
	}
}

func TestSessionWriteKicksBackgroundPush(t *testing.T) {
	e := newEnv(t)
	e.putFile("app", fileIndex, "3\topen\t\t\t2026-02-03T00:00:00Z\tThird thing\n")
	spawn := filepath.Join(t.TempDir(), "spawn.log")
	e.extra["ISSUES_NO_AUTOSYNC"] = ""
	e.extra["ISSUES_TEST_SPAWN"] = spawn
	s, err := NewSession(e.cfg, "", Env{Getenv: func(k string) string { return e.extra[k] }, Cwd: "/", Now: func() time.Time { return e.now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Do("comment", "app#3", "me", []string{"hello"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(spawn)
	eq(t, "tui write spawns", string(b), "spawn\n")
}
