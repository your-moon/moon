package issues

import (
	"os"
	"strings"
	"testing"
	"time"
)

// writeLeaseCfg replaces the test config; claimTTL "" leaves the key out.
func writeLeaseCfg(e *env, claimTTL string) {
	body := "mirror_dir = \"mirror\"\n"
	if claimTTL != "" {
		body += "claim_ttl = \"" + claimTTL + "\"\n"
	}
	body += "[repos.app]\ngithub = \"example/app\"\n[repos.lib]\ngithub = \"example/lib\"\n"
	if err := os.WriteFile(e.cfg, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// The lease a claim gets when --ttl is not given comes from claim_ttl, so a project can make an orphaned claim
// (an agent that was killed and never released) stop looking like work in half an hour instead of two hours.
func TestClaimTTLFromConfig(t *testing.T) {
	e := newEnv(t)
	seedIndex(e)
	writeLeaseCfg(e, "10m")
	if out, _, code := e.run("claim", "app#3", "--agent", "a1"); code != 0 || out != "claimed app#3 for a1\n" {
		t.Fatalf("%d %q", code, out)
	}
	e.now = e.now.Add(10*time.Minute - time.Second)
	if _, _, code := e.run("claim", "app#3", "--agent", "a2"); code != 3 {
		t.Fatal("claim must still be fresh one second before the configured 10m lease ends")
	}
	e.now = e.now.Add(2 * time.Second)
	out, _, code := e.run("claim", "app#3", "--agent", "a2")
	if code != 0 || !strings.HasPrefix(out, "took over stale claim on app#3 (was ") {
		t.Fatalf("a claim past the configured lease must be stale: %d %q", code, out)
	}
}

func TestClaimTTLFlagBeatsConfig(t *testing.T) {
	e := newEnv(t)
	seedIndex(e)
	writeLeaseCfg(e, "10m")
	e.run("claim", "app#3", "--agent", "a1", "--ttl", "60m")
	e.now = e.now.Add(11 * time.Minute)
	if _, _, code := e.run("claim", "app#3", "--agent", "a2"); code != 3 {
		t.Fatal("an explicit --ttl 60m must win over claim_ttl 10m")
	}
}

func TestClaimTTLDefaultsToTwoHoursWithoutConfig(t *testing.T) {
	e := newEnv(t)
	seedIndex(e)
	e.run("claim", "app#3", "--agent", "a1")
	e.now = e.now.Add(119 * time.Minute)
	if _, _, code := e.run("claim", "app#3", "--agent", "a2"); code != 3 {
		t.Fatal("without claim_ttl the lease stays 120m")
	}
	e.now = e.now.Add(2 * time.Minute)
	if _, _, code := e.run("claim", "app#3", "--agent", "a2"); code != 0 {
		t.Fatal("past 120m the claim is stale")
	}
}

func TestClaimTTLBadConfigIsRejected(t *testing.T) {
	e := newEnv(t)
	seedIndex(e)
	writeLeaseCfg(e, "soon")
	_, errs, code := e.run("claims")
	if code == 0 || !strings.Contains(errs, "claim_ttl must look like") {
		t.Fatalf("a bad claim_ttl must stop the command with a clear message: %d %q", code, errs)
	}
}

// Stopping work: the agent is gone and cannot release. sweep alone never touches a live claim (that safety stays);
// --live lists them, --release drops them, --agent narrows it to one holder.
func TestSweepLiveAndAgent(t *testing.T) {
	e := newEnv(t)
	workFixture(e)
	e.putClaim("app", "23", `{"agent": "x", "pid": 0, "host": "box", "ts": 1772366000, "ttl": 7200}`+"\n")

	out, _, _ := e.run("sweep")
	if strings.Contains(out, "app#21") || strings.Contains(out, "app#23") {
		t.Fatalf("plain sweep must not list live claims:\n%s", out)
	}

	out, _, code := e.run("sweep", "--live")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"app#21\tlive claim, released on request\tsonnet-17", "app#23\tlive claim, released on request\tx", "app#22\tstale", "app#24\tissue closed"} {
		if !strings.Contains(out, want) {
			t.Errorf("--live dry run lacks %q:\n%s", want, out)
		}
	}
	if !e.hasClaim("app", "21") || !e.hasClaim("app", "23") {
		t.Fatal("a dry run removed a claim")
	}

	out, _, _ = e.run("sweep", "--live", "--agent", "sonnet-17", "--release")
	if !strings.Contains(out, "released\tapp#21") || strings.Contains(out, "app#23") || strings.Contains(out, "app#22") {
		t.Errorf("--agent must keep only that holder's claims:\n%s", out)
	}
	if e.hasClaim("app", "21") {
		t.Error("the named agent's live claim was not released")
	}
	if !e.hasClaim("app", "23") || !e.hasClaim("app", "22") || !e.hasClaim("app", "24") {
		t.Error("another holder's claim was removed")
	}
}
