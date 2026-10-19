package issues

import (
	"os"
	"path/filepath"
	"testing"
)

func TestActionAllows(t *testing.T) {
	live := func(agent string) *ClaimInfo { return &ClaimInfo{Agent: agent} }
	stale := &ClaimInfo{Agent: "bob", Stale: true}
	cases := []struct {
		action string
		it     Item
		agent  string
		want   bool
	}{
		{"claim", Item{State: "open"}, "me", true},
		{"claim", Item{State: "open", Claim: stale}, "me", true},
		{"claim", Item{State: "open", Claim: live("me")}, "me", true}, // own claim: renew
		{"claim", Item{State: "open", Claim: live("bob")}, "me", false},
		{"claim", Item{State: "closed"}, "me", false},
		{"release", Item{State: "open"}, "me", false},
		{"release", Item{State: "open", Claim: live("bob")}, "me", true},
		{"close", Item{State: "open"}, "me", true},
		{"close", Item{State: "closed"}, "me", false},
		{"reopen", Item{State: "closed"}, "me", true},
		{"reopen", Item{State: "open"}, "me", false},
		{"comment", Item{State: "closed"}, "me", true},
	}
	for _, c := range cases {
		a, ok := ActionNamed(c.action)
		if !ok {
			t.Fatalf("no action %q", c.action)
		}
		if got := a.Allows(c.it, c.agent); got != c.want {
			t.Errorf("%s on %+v as %s: got %v want %v", c.action, c.it, c.agent, got, c.want)
		}
	}
}

// Every write path starts the background push where the op is queued: the CLI and the dashboard's Session
// share it (a read's own autosync is BeforeRead's, not this).
func TestCLIQueuedWriteKicksPushOnce(t *testing.T) {
	e := newEnv(t)
	e.putFile("app", fileIndex, "3\topen\t\t\t2026-02-03T00:00:00Z\tThird thing\n")
	spawn := filepath.Join(t.TempDir(), "spawn.log")
	delete(e.extra, "ISSUES_NO_AUTOSYNC")
	e.extra["ISSUES_TEST_SPAWN"] = spawn
	count := func() int {
		b, _ := os.ReadFile(spawn)
		n := 0
		for _, c := range b {
			if c == '\n' {
				n++
			}
		}
		return n
	}
	if _, _, code := e.run("comment", "app#3", "--body", "hi"); code != 0 {
		t.Fatal(code)
	}
	if count() != 1 {
		t.Fatalf("queued comment spawned %d pushes, want 1", count())
	}
}
