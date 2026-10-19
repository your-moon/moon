package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/your-moon/moon/internal/issues"
)

// scopedShell is a dashboard over repos app and lib, each with its own milestone and claim.
func scopedShell(t *testing.T, sc issues.Scope) Shell {
	mi := newMirror(t, base, "app", "lib")
	mi.milestones("app", "appms\topen\t1\t1\t2026-03-10T00:00:00Z")
	mi.milestones("lib", "libms\topen\t1\t1\t2026-03-11T00:00:00Z")
	mi.claim("app#2", "robin", 600, 7200)
	mi.claim("lib#2", "sam", 600, 7200)
	var s tea.Model = NewShell([]Tab{
		NewIssuesTab(mi.sess, Options{Agent: "me", MirrorDir: "mirror", Scope: sc}),
		NewMilestonesTab(mi.sess),
		NewClaimsTab(mi.sess),
	}, 0)
	s, _ = s.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	return s.(Shell)
}

// seen reports which of the two repos' milestone titles and claim refs the tab shows.
func seen(s Shell, tab string) (app, lib bool) {
	s = sdo(s, tab)
	v := s.View()
	return strings.Contains(v, "appms") || strings.Contains(v, "app#2"), strings.Contains(v, "libms") || strings.Contains(v, "lib#2")
}

func TestTabsFollowTheScope(t *testing.T) {
	project := issues.Scope{Repos: []string{"lib"}, Kind: issues.KindProject, Reason: "paths in config"}
	for _, tab := range []string{"2", "3"} {
		s := scopedShell(t, project)
		if a, l := seen(s, tab); a || !l {
			t.Fatalf("tab %s: a project scope shows lib only, got app=%v lib=%v", tab, a, l)
		}
		if v := sdo(s, tab).View(); !strings.Contains(v, "scope: lib (paths in config)") {
			t.Errorf("tab %s footer lacks the scope:\n%s", tab, v)
		}
		// a toggles to every repo from the tab itself, and back
		all := sdo(s, tab, "a")
		if a, l := seen(all, tab); !a || !l {
			t.Fatalf("tab %s: a shows every repo, got app=%v lib=%v", tab, a, l)
		}
		if v := all.View(); !strings.Contains(v, "scope: all repos (a: back to lib)") {
			t.Errorf("tab %s footer after a:\n%s", tab, v)
		}
		if a, l := seen(sdo(all, "a"), tab); a || !l {
			t.Fatalf("tab %s: a again returns to lib, got app=%v lib=%v", tab, a, l)
		}
		// tab walks the repos like the Issues tab: lib is last, so it wraps to every repo, and the next tab press is app
		wrap := sdo(s, tab, "tab")
		if a, l := seen(wrap, tab); !a || !l {
			t.Fatalf("tab %s: tab from the last repo wraps to all, got app=%v lib=%v", tab, a, l)
		}
		if a, l := seen(sdo(wrap, "tab"), tab); !a || l {
			t.Fatalf("tab %s: tab from all picks the first repo, got app=%v lib=%v", tab, a, l)
		}
	}
}

func TestScopeSetInIssuesCarriesToTabs(t *testing.T) {
	project := issues.Scope{Repos: []string{"lib"}, Kind: issues.KindProject, Reason: "paths in config"}
	s := sdo(scopedShell(t, project), "a") // on the Issues tab
	for _, tab := range []string{"2", "3"} {
		if a, l := seen(s, tab); !a || !l {
			t.Fatalf("tab %s after a in Issues: app=%v lib=%v", tab, a, l)
		}
	}
	// :filter repo:app and :filter clear, typed in the Issues tab
	s = sdo(scopedShell(t, project), ":")
	for _, r := range "filter repo:app" {
		s = sdo(s, string(r))
	}
	nm, _ := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = nm.(Shell)
	for _, tab := range []string{"2", "3"} {
		if a, l := seen(s, tab); !a || l {
			t.Fatalf("tab %s after :filter repo:app: app=%v lib=%v", tab, a, l)
		}
	}
}

func TestAllReposShowsEveryRepoInEveryTab(t *testing.T) {
	global := issues.Scope{Repos: []string{"app", "lib"}, Kind: issues.KindGlobal, Reason: "--all"}
	s := scopedShell(t, global)
	for _, tab := range []string{"2", "3"} {
		if a, l := seen(s, tab); !a || !l {
			t.Fatalf("tab %s with --all: app=%v lib=%v", tab, a, l)
		}
	}
}
