package completion

import (
	"testing"

	"github.com/your-moon/moon/internal/issues"
)

// asAgent is the fixture's options with $USER set, as a shell completion run would have it.
func (f *fixture) asAgent(user string) Options {
	o := f.opts()
	o.Open = func(cfg, dir string) Source {
		if cfg == "" {
			cfg = f.cfg
		}
		return OpenSession(cfg, dir, issues.Env{Getenv: func(k string) string {
			if k == "USER" {
				return user
			}
			return ""
		}, Cwd: f.dir})
	}
	return o
}

// moon#41: release completion offers only the claims the agent may release.
func TestReleaseOffersOnlyOwnClaims(t *testing.T) {
	f := newFixture(t, standard) // erp#2 is alice's, erp#510 is bob's
	want(t, "as bob", list(Complete(f.asAgent("bob"), []string{"issues", "release", ""})), "erp#510")
	want(t, "as alice", list(Complete(f.asAgent("alice"), []string{"issues", "release", ""})), "erp#2")
	want(t, "as nobody", list(Complete(f.asAgent("carol"), []string{"issues", "release", ""})), "")
	want(t, "claim as bob renews his own", list(Complete(f.asAgent("bob"), []string{"issues", "claim", "erp#510"})), "erp#510")
}
