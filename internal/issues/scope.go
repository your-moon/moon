package issues

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Project scope: which repos a command looks at when the user did not say. Resolved once at startup by
// ResolveScope, a pure function of the working directory, the config and (through a function) the git remote.

// Alias is a [repos.<alias>] name from moon.toml.
type Alias = string

// ScopeKind says how a scope was decided.
type ScopeKind string

const (
	KindProject  ScopeKind = "project"  // the working directory belongs to one project (config, path mapping or git remote)
	KindGlobal   ScopeKind = "global"   // every configured repo
	KindExplicit ScopeKind = "explicit" // -R/--repo on the command line
)

// Scope is the set of repos a command defaults to.
type Scope struct {
	Repos  []Alias
	Reason string // short human reason, shown after the repos: "git remote origin", "not inside a project"
	Kind   ScopeKind
}

// All reports whether the scope is every configured repo.
func (s Scope) All() bool { return s.Kind == KindGlobal }

// Label is the repos as the header prints them.
func (s Scope) Label() string {
	if s.Kind == KindGlobal {
		return "all repos"
	}
	return strings.Join(s.Repos, ", ")
}

// GitRemoteFunc returns the URL of remote "origin" of the git checkout that contains dir.
type GitRemoteFunc func(dir string) (string, error)

// ScopeOpts is what the command line and the environment contribute to resolution.
type ScopeOpts struct {
	Flags []string // values of -R/--repo, in order; "all" forces global
	All   bool     // --all
	Home  string   // $HOME, for "~" in paths
}

// RuleLine is one rule of the resolution order with what it found, for `moon issues where`.
type RuleLine struct {
	Rule   string
	Repos  []Alias
	Detail string
}

// ResolveScope picks the scope, first match wins:
//  1. -R/--repo flags (explicit); -R all or --all forces global
//  2. the config file in the working directory or a parent, when it declares exactly one repo
//  3. paths = [...] of a repo that contain the working directory, longest path wins
//  4. the git remote "origin", matched case-insensitively against every [repos.*] github
//  5. global: all configured repos
func ResolveScope(cwd string, cfg *Config, git GitRemoteFunc, opt ScopeOpts) Scope {
	s, _ := resolve(cwd, cfg, git, opt, false)
	return s
}

// ExplainScope evaluates every rule, not only up to the first match.
func ExplainScope(cwd string, cfg *Config, git GitRemoteFunc, opt ScopeOpts) (Scope, []RuleLine) {
	return resolve(cwd, cfg, git, opt, true)
}

func resolve(cwd string, cfg *Config, git GitRemoteFunc, opt ScopeOpts, full bool) (Scope, []RuleLine) {
	var lines []RuleLine
	var won *Scope
	take := func(l RuleLine, kind ScopeKind, reason string) {
		lines = append(lines, l)
		if won == nil && len(l.Repos) > 0 {
			won = &Scope{Repos: l.Repos, Kind: kind, Reason: reason}
		}
	}
	// the later rules only run when needed, unless the caller wants the whole picture
	need := func() bool { return full || won == nil }
	allAliases := make([]Alias, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		allAliases = append(allAliases, r.Alias)
	}

	// 1. flags
	flag := RuleLine{Rule: "flag -R/--repo/--all", Detail: "not given"}
	kind, reason := KindExplicit, ""
	switch {
	case opt.All || contains(opt.Flags, "all"):
		flag.Repos, kind = allAliases, KindGlobal
		reason = "-R all"
		if opt.All {
			reason = "--all"
		}
		flag.Detail = reason + ": every repo"
	case len(opt.Flags) > 0:
		for _, f := range opt.Flags {
			if !contains(flag.Repos, f) {
				flag.Repos = append(flag.Repos, f)
			}
		}
		reason = "-R " + strings.Join(flag.Repos, ",")
		flag.Detail = "given: " + strings.Join(flag.Repos, ", ")
	}
	take(flag, kind, reason)

	real := realPath(cwd)

	// 2. the project's own config
	pc := RuleLine{Rule: "project config"}
	if need() {
		root := configRoot(cfg.Path)
		switch {
		case cfg.Path == "":
			pc.Detail = "no config file"
		case !within(realPath(root), real):
			pc.Detail = cfg.Path + " is not in or above " + real + " (user or --config file)"
		case len(cfg.Repos) != 1:
			pc.Detail = cfg.Path + " declares " + countOf(len(cfg.Repos), "repo") + ", needs exactly one"
		default:
			pc.Repos = []Alias{cfg.Repos[0].Alias}
			pc.Detail = cfg.Path + " declares only " + cfg.Repos[0].Alias
		}
	} else {
		pc.Detail = "skipped"
	}
	take(pc, KindProject, "project config")

	// 3. path mapping
	pm := RuleLine{Rule: "paths in config"}
	if need() {
		best, hits := -1, []string{}
		var who []Alias
		for _, r := range cfg.Repos {
			for _, p := range r.Paths {
				ep, ok := expandPath(p, opt.Home, cfg.Dir)
				if !ok || !within(ep, real) {
					continue
				}
				switch {
				case len(ep) > best:
					best, who, hits = len(ep), []Alias{r.Alias}, []string{r.Alias + " " + p}
				case len(ep) == best:
					if !contains(who, r.Alias) {
						who = append(who, r.Alias)
					}
					hits = append(hits, r.Alias+" "+p)
				}
			}
		}
		if len(who) > 0 {
			pm.Repos = who
			pm.Detail = "longest match: " + strings.Join(hits, ", ")
		} else {
			pm.Detail = "no repo declares a path that contains " + real
		}
	} else {
		pm.Detail = "skipped"
	}
	take(pm, KindProject, "paths in config")

	// 4. git remote
	gr := RuleLine{Rule: "git remote origin"}
	if need() {
		gr = gitRule(cwd, cfg, git)
	} else {
		gr.Detail = "skipped"
	}
	take(gr, KindProject, "git remote origin")

	gl := RuleLine{Rule: "global", Repos: allAliases, Detail: "every configured repo"}
	lines = append(lines, gl)
	if won == nil {
		won = &Scope{Repos: allAliases, Kind: KindGlobal, Reason: "not inside a project"}
	}
	return *won, lines
}

func gitRule(cwd string, cfg *Config, git GitRemoteFunc) RuleLine {
	gr := RuleLine{Rule: "git remote origin"}
	hasGH := false
	for _, r := range cfg.Repos {
		if r.GitHub != "" {
			hasGH = true
		}
	}
	if !hasGH {
		gr.Detail = "no repo has a github value (local-only repos match by config or paths only)"
		return gr
	}
	if git == nil {
		gr.Detail = "no git lookup"
		return gr
	}
	raw, err := git(cwd)
	raw = strings.TrimSpace(raw)
	if err != nil || raw == "" {
		gr.Detail = "no remote origin (not a git checkout, or it has no origin)"
		return gr
	}
	ownerName, ok := ParseGitRemote(raw)
	if !ok {
		gr.Detail = "remote origin is not an owner/name URL"
		return gr
	}
	for _, r := range cfg.Repos {
		if r.GitHub != "" && strings.EqualFold(r.GitHub, ownerName) {
			gr.Repos = append(gr.Repos, r.Alias)
		}
	}
	if len(gr.Repos) == 0 {
		gr.Detail = "remote origin is " + ownerName + ", which no [repos.*] github declares"
	} else {
		gr.Detail = "remote origin is " + ownerName
	}
	return gr
}

// ParseGitRemote turns a remote URL into "owner/name". It reads ssh (git@host:owner/name.git,
// ssh://git@host/owner/name) and https forms and ignores the host and any credentials in the URL.
func ParseGitRemote(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	var path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		path = u.Path
	} else {
		i := strings.Index(raw, ":")
		if i <= 0 || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") {
			return "", false
		}
		path = raw[i+1:]
	}
	path = strings.Trim(path, "/")
	if strings.HasSuffix(strings.ToLower(path), ".git") {
		path = path[:len(path)-4]
	}
	path = strings.Trim(path, "/")
	segs := strings.Split(path, "/")
	if len(segs) < 2 {
		return "", false
	}
	owner, name := segs[len(segs)-2], segs[len(segs)-1]
	if owner == "" || name == "" || !githubRe.MatchString(owner+"/"+name) {
		return "", false
	}
	return owner + "/" + name, true
}

// GitRemoteOrigin is the real GitHubRemoteFunc: `git remote get-url origin`, local only, one second at most.
func GitRemoteOrigin(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// configRoot is the folder a config file belongs to: its own, or the parent of a .moon folder.
func configRoot(cfgPath string) string {
	if cfgPath == "" {
		return ""
	}
	d := filepath.Dir(cfgPath)
	if filepath.Base(d) == ".moon" {
		return filepath.Dir(d)
	}
	return d
}

// expandPath resolves one `paths` entry: "~" is home, a relative path is relative to the config's folder.
func expandPath(p, home, cfgDir string) (string, bool) {
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		if home == "" {
			return "", false
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	case !filepath.IsAbs(p):
		if cfgDir == "" {
			return "", false
		}
		p = filepath.Join(cfgDir, p)
	}
	return realPath(p), true
}

// realPath is p made absolute with symlinks resolved; when p does not exist, the cleaned absolute path.
func realPath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// within reports whether p is dir or inside it, on whole path segments (/a/app does not contain /a/app2).
func within(dir, p string) bool {
	if dir == "" {
		return false
	}
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

func countOf(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
