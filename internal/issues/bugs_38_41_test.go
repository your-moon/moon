package issues

import (
	"os"
	"strings"
	"testing"
	"time"
)

func (e *env) session() *Session {
	e.t.Helper()
	s, err := NewSession(e.cfg, "", Env{Getenv: func(k string) string { return e.extra[k] }, Cwd: "/", Now: func() time.Time { return e.now }})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func prependCfg(e *env, line string) {
	b, _ := os.ReadFile(e.cfg)
	os.WriteFile(e.cfg, append([]byte(line), b...), 0o644)
}

// moon#38: a TUI claim takes its lease from claim_ttl like the CLI.
func TestSessionClaimUsesClaimTTL(t *testing.T) {
	e := newEnv(t)
	seedOverlay(e)
	prependCfg(e, "claim_ttl = \"30m\"\n")
	e.session().Do("claim", "app#3", "tui", nil)
	out, _, _ := e.run("claims")
	if !strings.Contains(out, "ttl=1800s") {
		t.Fatalf("TUI claim lease not from claim_ttl:\n%s", out)
	}
}

// moon#38: autosync = false stops a Session write from starting a background sync.
func TestSessionWriteHonoursAutosyncFalse(t *testing.T) {
	e := newEnv(t)
	seedOverlay(e)
	prependCfg(e, "autosync = false\n")
	delete(e.extra, "ISSUES_NO_AUTOSYNC")
	spawn := e.home + "-spawn"
	e.extra["ISSUES_TEST_SPAWN"] = spawn
	e.session().Do("comment", "app#3", "tui", []string{"hi"})
	if b, err := os.ReadFile(spawn); err == nil {
		t.Fatalf("Session write spawned a sync despite autosync = false: %q", b)
	}
}
