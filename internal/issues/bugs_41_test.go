package issues

import (
	"errors"
	"testing"
)

// moon#41: Do enforces Allows, so a caller needs no separate check.
func TestDoEnforcesAllows(t *testing.T) {
	e := newEnv(t)
	seedIssue7(e)
	e.run("claim", "app#7", "--agent", "robin")
	s := e.session()
	if _, err := s.Do("release", "app#7", "bob", nil); !errors.Is(err, ErrHeld) {
		t.Fatalf("release of robin's claim as bob: %v, want ErrHeld", err)
	}
	if _, err := s.Do("claim", "app#7", "bob", nil); !errors.Is(err, ErrHeld) {
		t.Fatalf("claim of robin's issue as bob: %v, want ErrHeld", err)
	}
	if out, _, _ := e.run("claims"); out == "" {
		t.Fatal("robin's claim was dropped")
	}
	if _, err := s.Do("reopen", "app#7", "bob", nil); err == nil {
		t.Fatal("reopen of an open issue was queued")
	}
	if n := len(e.app().loadOutbox()); n != 0 {
		t.Fatalf("a refused action queued %d ops", n)
	}
	if _, err := s.Do("release", "app#7", "robin", nil); err != nil {
		t.Fatalf("release of own claim: %v", err)
	}
	if _, err := s.Do("release", "app#7", "robin", nil); err == nil {
		t.Fatal("release without a claim was accepted")
	}
}
