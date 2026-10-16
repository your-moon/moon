package issues

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cfgAt writes toml into dir/rel and loads it.
func cfgAt(t *testing.T, dir, rel, toml string) *Config {
	t.Helper()
	p := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fakeGit(url string) GitRemoteFunc {
	return func(string) (string, error) {
		if url == "" {
			return "", fmt.Errorf("fatal: not a git repository")
		}
		return url + "\n", nil
	}
}

func mkdirs(t *testing.T, base string, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if err := os.MkdirAll(filepath.Join(base, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

const twoRepos = `
[repos.app]
github = "acme/app"
[repos.lib]
github = "acme/lib"
`

func TestResolveScopeRules(t *testing.T) {
	root := realPath(t.TempDir())
	mkdirs(t, root, "work/app/src/deep", "work/app2", "work/lib", "work/misc", "proj/sub", "other")
	home := filepath.Join(root, "home")
	mkdirs(t, root, "home/dev/app")
	userCfgDir := filepath.Join(root, "userconf")
	mkdirs(t, root, "userconf")

	// a config that lives far from the cwd (the user config) and declares two repos with path mappings
	mapped := cfgAt(t, userCfgDir, "moon.toml", `
[repos.app]
github = "acme/app"
paths = ["`+root+`/work/app", "~/dev/app"]
[repos.lib]
github = "acme/lib"
paths = ["`+root+`/work"]
[repos.notes]
local = true
paths = ["`+root+`/work/misc"]
`)
	// the same repos without paths
	plain := cfgAt(t, userCfgDir, "plain.toml", twoRepos+"[repos.notes]\nlocal = true\n")
	// two repos called app under different owners
	twins := cfgAt(t, userCfgDir, "twins.toml", "[repos.mine]\ngithub = \"me/app\"\n[repos.theirs]\ngithub = \"them/app\"\n")
	// a project-local config declaring exactly one repo, and a project-local one declaring two
	single := cfgAt(t, filepath.Join(root, "proj"), "moon.toml", "[repos.only]\ngithub = \"acme/only\"\n")
	dotMoon := cfgAt(t, filepath.Join(root, "proj"), ".moon/moon.toml", "[repos.hidden]\ngithub = \"acme/hidden\"\n")
	_ = dotMoon
	pair := cfgAt(t, filepath.Join(root, "proj"), "pair.toml", twoRepos)
	_ = pair

	type tc struct {
		name  string
		cwd   string
		cfg   *Config
		git   string
		opt   ScopeOpts
		repos string
		kind  ScopeKind
		why   string
	}
	app := filepath.Join(root, "work/app")
	for _, c := range []tc{
		// 1. flags
		{"flag explicit", root, plain, "", ScopeOpts{Flags: []string{"lib"}}, "lib", KindExplicit, "-R lib"},
		{"flags several, deduped, in order", root, plain, "", ScopeOpts{Flags: []string{"lib", "app", "lib"}}, "lib,app", KindExplicit, "-R lib,app"},
		{"flag beats git remote", root, plain, "git@github.com:acme/app.git", ScopeOpts{Flags: []string{"lib"}}, "lib", KindExplicit, "-R lib"},
		{"flag beats path mapping", app, mapped, "", ScopeOpts{Flags: []string{"lib"}}, "lib", KindExplicit, "-R lib"},
		{"flag beats project config", filepath.Join(root, "proj/sub"), single, "", ScopeOpts{Flags: []string{"lib"}}, "lib", KindExplicit, "-R lib"},
		{"-R all forces global", app, mapped, "git@github.com:acme/app.git", ScopeOpts{Flags: []string{"all"}}, "app,lib,notes", KindGlobal, "-R all"},
		{"--all forces global", app, mapped, "git@github.com:acme/app.git", ScopeOpts{All: true}, "app,lib,notes", KindGlobal, "--all"},
		{"--all beats -R", app, mapped, "", ScopeOpts{All: true, Flags: []string{"lib"}}, "app,lib,notes", KindGlobal, "--all"},
		// 2. the project's own config
		{"project config, at its root", filepath.Join(root, "proj"), single, "", ScopeOpts{}, "only", KindProject, "project config"},
		{"project config, nested dir", filepath.Join(root, "proj/sub"), single, "", ScopeOpts{}, "only", KindProject, "project config"},
		{"project config beats path mapping and git", filepath.Join(root, "proj/sub"), single, "git@github.com:acme/app.git", ScopeOpts{}, "only", KindProject, "project config"},
		{"project config in .moon", filepath.Join(root, "proj/sub"), dotMoon, "", ScopeOpts{}, "hidden", KindProject, "project config"},
		{"project config outside the cwd tree is not a project", filepath.Join(root, "other"), single, "", ScopeOpts{}, "only", KindGlobal, "not inside a project"},
		{"project config with two repos does not decide", filepath.Join(root, "proj/sub"), pair, "", ScopeOpts{}, "app,lib", KindGlobal, "not inside a project"},
		{"project config with two repos falls to git", filepath.Join(root, "proj/sub"), pair, "https://github.com/acme/lib", ScopeOpts{}, "lib", KindProject, "git remote origin"},
		// 3. path mapping
		{"path mapping, exact", app, mapped, "", ScopeOpts{Home: home}, "app", KindProject, "paths in config"},
		{"path mapping, nested", filepath.Join(app, "src/deep"), mapped, "", ScopeOpts{Home: home}, "app", KindProject, "paths in config"},
		{"path mapping, longest prefix wins over a shorter one", filepath.Join(app, "src"), mapped, "", ScopeOpts{Home: home}, "app", KindProject, "paths in config"},
		{"path mapping, shorter prefix when the longer does not match", filepath.Join(root, "work/lib"), mapped, "", ScopeOpts{Home: home}, "lib", KindProject, "paths in config"},
		{"path mapping is on whole segments (app2 is not app)", filepath.Join(root, "work/app2"), mapped, "", ScopeOpts{Home: home}, "lib", KindProject, "paths in config"},
		{"path mapping, tilde", filepath.Join(home, "dev/app"), mapped, "", ScopeOpts{Home: home}, "app", KindProject, "paths in config"},
		{"path mapping, tilde without a home is skipped", filepath.Join(home, "dev/app"), mapped, "", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
		{"path mapping reaches a local-only repo", filepath.Join(root, "work/misc"), mapped, "", ScopeOpts{Home: home}, "notes", KindProject, "paths in config"},
		{"path mapping beats git remote", app, mapped, "git@github.com:acme/lib.git", ScopeOpts{Home: home}, "app", KindProject, "paths in config"},
		{"outside every path", filepath.Join(root, "other"), mapped, "", ScopeOpts{Home: home}, "app,lib,notes", KindGlobal, "not inside a project"},
		// 4. git remote
		{"ssh remote", root, plain, "git@github.com:acme/app.git", ScopeOpts{}, "app", KindProject, "git remote origin"},
		{"https remote", root, plain, "https://github.com/acme/lib", ScopeOpts{}, "lib", KindProject, "git remote origin"},
		{"https remote with .git and different case", root, plain, "https://GitHub.com/ACME/App.GIT", ScopeOpts{}, "app", KindProject, "git remote origin"},
		{"ssh:// remote with a port", root, plain, "ssh://git@github.com:22/acme/app.git", ScopeOpts{}, "app", KindProject, "git remote origin"},
		{"https remote with credentials", root, plain, "https://user:secret@github.com/acme/lib.git", ScopeOpts{}, "lib", KindProject, "git remote origin"},
		{"ssh host alias", root, plain, "git@github-work:acme/lib.git", ScopeOpts{}, "lib", KindProject, "git remote origin"},
		{"no remote", root, plain, "", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
		{"non-git directory", filepath.Join(root, "other"), plain, "", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
		{"remote nobody declares", root, plain, "git@github.com:acme/zzz.git", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
		{"owner is matched whole, not as a substring", root, plain, "git@github.com:acme-corp/app.git", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
		{"owner prefix is not a match either", root, plain, "git@github.com:ac/app.git", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
		{"name is matched whole", root, plain, "git@github.com:acme/app-extra.git", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
		{"same name, first owner", root, twins, "git@github.com:me/app.git", ScopeOpts{}, "mine", KindProject, "git remote origin"},
		{"same name, second owner", root, twins, "git@github.com:them/app.git", ScopeOpts{}, "theirs", KindProject, "git remote origin"},
		{"same name, third owner", root, twins, "git@github.com:someone/app.git", ScopeOpts{}, "mine,theirs", KindGlobal, "not inside a project"},
		{"local-only repo is never matched by the remote", root, cfgAt(t, userCfgDir, "loc.toml", "[repos.notes]\nlocal = true\n[repos.x]\ngithub = \"acme/x\"\n"), "git@github.com:acme/notes.git", ScopeOpts{}, "notes,x", KindGlobal, "not inside a project"},
		// 5. global
		{"global lists every repo in file order", root, plain, "", ScopeOpts{}, "app,lib,notes", KindGlobal, "not inside a project"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveScope(c.cwd, c.cfg, fakeGit(c.git), c.opt)
			if strings.Join(got.Repos, ",") != c.repos || got.Kind != c.kind || got.Reason != c.why {
				t.Fatalf("got repos=%v kind=%s reason=%q\nwant repos=%s kind=%s reason=%q", got.Repos, got.Kind, got.Reason, c.repos, c.kind, c.why)
			}
		})
	}
}

func TestResolveScopeSymlinkedCwd(t *testing.T) {
	root := realPath(t.TempDir())
	mkdirs(t, root, "real/app/src")
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	cfg := cfgAt(t, filepath.Join(root, "conf"), "moon.toml", "[repos.app]\ngithub = \"acme/app\"\npaths = [\""+root+"/real/app\"]\n[repos.lib]\ngithub = \"acme/lib\"\n")
	got := ResolveScope(filepath.Join(root, "link/app/src"), cfg, fakeGit(""), ScopeOpts{})
	if strings.Join(got.Repos, ",") != "app" || got.Reason != "paths in config" {
		t.Fatalf("a symlinked cwd must match on its real path: %+v", got)
	}
}

func TestPathsRelativeToConfig(t *testing.T) {
	root := realPath(t.TempDir())
	mkdirs(t, root, "ws/app/x", "ws/lib")
	cfg := cfgAt(t, filepath.Join(root, "ws"), "moon.toml", "[repos.app]\ngithub = \"acme/app\"\npaths = [\"app\"]\n[repos.lib]\ngithub = \"acme/lib\"\npaths = [\"lib\"]\n")
	got := ResolveScope(filepath.Join(root, "ws/app/x"), cfg, fakeGit(""), ScopeOpts{})
	if strings.Join(got.Repos, ",") != "app" {
		t.Fatalf("relative paths are relative to the config folder: %+v", got)
	}
}

func TestParseGitRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/app.git":               "acme/app",
		"git@github.com:acme/app":                   "acme/app",
		"https://github.com/acme/app":               "acme/app",
		"https://github.com/acme/app/":              "acme/app",
		"https://github.com/acme/app.git":           "acme/app",
		"http://github.com/Acme/App.git":            "Acme/App",
		"ssh://git@github.com/acme/app.git":         "acme/app",
		"ssh://git@github.com:2222/acme/app.git":    "acme/app",
		"git://github.com/acme/app.git":             "acme/app",
		"https://tok:x@github.com/acme/app.git":     "acme/app",
		"git@gh-alias:acme/app.git":                 "acme/app",
		"https://git.example.org/group/sub/app.git": "sub/app",
		"/srv/git/app.git":                          "",
		"../app":                                    "",
		"app":                                       "",
		"":                                          "",
		"git@github.com:app.git":                    "",
	} {
		got, ok := ParseGitRemote(in)
		if got != want || ok != (want != "") {
			t.Errorf("ParseGitRemote(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}

func TestPathsKeyParsing(t *testing.T) {
	d := t.TempDir()
	c := cfgAt(t, d, "moon.toml", "[repos.app]\ngithub = \"a/b\"\npaths = [\"~/x\", \"/abs/y\"]  # note\n[repos.lib]\nlocal = true\n")
	if got := strings.Join(c.Repos[0].Paths, "|"); got != "~/x|/abs/y" || c.Repos[1].Paths != nil {
		t.Fatalf("paths: %q %v", got, c.Repos[1].Paths)
	}
	for _, bad := range []string{`paths = "x"`, `paths = ["a" "b"]`, `paths = ["a`, `paths = [a]`} {
		p := filepath.Join(d, "bad.toml")
		os.WriteFile(p, []byte("[repos.app]\ngithub = \"a/b\"\n"+bad+"\n"), 0o644)
		if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "paths") {
			t.Errorf("%q: want a paths error, got %v", bad, err)
		}
	}
	p := filepath.Join(d, "all.toml")
	os.WriteFile(p, []byte("[repos.all]\ngithub = \"a/b\"\n"), 0o644)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("alias all must be reserved: %v", err)
	}
}

func TestRealGitRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := realPath(t.TempDir())
	mkdirs(t, root, "co/sub/deeper", "plain")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", filepath.Join(root, "co")}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("remote", "add", "origin", "git@github.com:Invented-Org/Widget.git")
	cfg := cfgAt(t, filepath.Join(root, "conf"), "moon.toml", "[repos.widget]\ngithub = \"invented-org/widget\"\n[repos.gadget]\ngithub = \"invented-org/gadget\"\n")
	for _, cwd := range []string{"co", "co/sub/deeper"} {
		got := ResolveScope(filepath.Join(root, cwd), cfg, GitRemoteOrigin, ScopeOpts{})
		if strings.Join(got.Repos, ",") != "widget" || got.Kind != KindProject || got.Reason != "git remote origin" {
			t.Errorf("%s: %+v", cwd, got)
		}
	}
	got := ResolveScope(filepath.Join(root, "plain"), cfg, GitRemoteOrigin, ScopeOpts{})
	if got.Kind != KindGlobal {
		t.Errorf("a folder that is not a git checkout must be global: %+v", got)
	}
}

func TestExplainScopeShowsEveryRule(t *testing.T) {
	root := realPath(t.TempDir())
	mkdirs(t, root, "work/app")
	cfg := cfgAt(t, filepath.Join(root, "conf"), "moon.toml", "[repos.app]\ngithub = \"acme/app\"\npaths = [\""+root+"/work/app\"]\n[repos.lib]\ngithub = \"acme/lib\"\n")
	sc, rules := ExplainScope(filepath.Join(root, "work/app"), cfg, fakeGit("git@github.com:acme/lib.git"), ScopeOpts{})
	if sc.Reason != "paths in config" || strings.Join(sc.Repos, ",") != "app" {
		t.Fatalf("winner: %+v", sc)
	}
	var m []string
	for _, r := range rules {
		m = append(m, r.Rule+"="+strings.Join(r.Repos, "+"))
	}
	want := "flag -R/--repo/--all=|project config=|paths in config=app|git remote origin=lib|global=app+lib"
	if strings.Join(m, "|") != want {
		t.Fatalf("rules:\n got %s\nwant %s", strings.Join(m, "|"), want)
	}
}
