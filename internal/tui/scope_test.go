package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/your-moon/moon/internal/issues"
)

func scopedModel(mi *mirror, sc issues.Scope) Model {
	m := New(mi.sess, Options{Agent: "me", MirrorDir: "mirror", Scope: sc})
	return send(m, tea.WindowSizeMsg{Width: 140, Height: 36})
}

func TestScopeOpensOnTheProjectRepo(t *testing.T) {
	mi := newMirror(t, base, "app", "lib")
	project := issues.Scope{Repos: []string{"lib"}, Kind: issues.KindProject, Reason: "git remote origin"}
	m := scopedModel(mi, project)
	if got := refs(m); got == "" || strings.Contains(got, "app#") || !strings.Contains(got, "lib#") {
		t.Fatalf("a project scope opens on its repo only: %q", got)
	}
	if !strings.Contains(m.View(), "scope: lib (git remote origin)") {
		t.Errorf("footer lacks the scope:\n%s", m.View())
	}
	// a shows every repo, a again goes back
	m = press(m, "a")
	if got := refs(m); !strings.Contains(got, "app#") || !strings.Contains(got, "lib#") {
		t.Fatalf("a toggles to all repos: %q", got)
	}
	if !strings.Contains(m.View(), "scope: all repos (a: back to lib)") {
		t.Errorf("footer after a:\n%s", m.View())
	}
	if sc := (modelSource{&m}).Scope(); len(sc) != 0 {
		t.Errorf("all repos: completion scope %v", sc)
	}
	m = press(m, "a")
	if got := refs(m); strings.Contains(got, "app#") {
		t.Fatalf("a again returns to the project: %q", got)
	}
	if sc := (modelSource{&m}).Scope(); len(sc) != 1 || sc[0] != "lib" {
		t.Errorf("tab completion follows the repo on screen: %v", sc)
	}
}

func TestScopeGlobalShowsEverything(t *testing.T) {
	mi := newMirror(t, base, "app", "lib")
	global := issues.Scope{Repos: []string{"app", "lib"}, Kind: issues.KindGlobal, Reason: "not inside a project"}
	m := scopedModel(mi, global)
	if got := refs(m); !strings.Contains(got, "app#") || !strings.Contains(got, "lib#") {
		t.Fatalf("global opens on all repos: %q", got)
	}
	if !strings.Contains(m.View(), "scope: all repos (not inside a project)") {
		t.Errorf("footer:\n%s", m.View())
	}
	// no scope given (tests, embedding): nothing preselected, nothing printed
	m = New(mi.sess, Options{Agent: "me"})
	m = send(m, tea.WindowSizeMsg{Width: 140, Height: 36})
	if strings.Contains(m.View(), "scope:") {
		t.Errorf("the zero scope prints nothing")
	}
}

func TestSessionResolvesScopeFromGit(t *testing.T) {
	d := realDir(t)
	cfg := filepath.Join(d, "moon.toml")
	os.WriteFile(cfg, []byte("[repos.app]\ngithub = \"example/app\"\n[repos.lib]\ngithub = \"example/lib\"\n"), 0o644)
	git := func(string) (string, error) { return "https://github.com/Example/Lib.git", nil }
	s, err := issues.NewSessionScoped(cfg, "", issues.Env{Getenv: func(string) string { return "" }, Cwd: d, GitRemote: git}, issues.ScopeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if sc := s.Scope(); sc.Kind != issues.KindProject || strings.Join(sc.Repos, ",") != "lib" {
		t.Errorf("scope: %+v", sc)
	}
	s, _ = issues.NewSessionScoped(cfg, "", issues.Env{Getenv: func(string) string { return "" }, Cwd: d, GitRemote: git}, issues.ScopeOpts{All: true})
	if sc := s.Scope(); sc.Kind != issues.KindGlobal {
		t.Errorf("--all: %+v", sc)
	}
}

func realDir(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
