package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The command side of project scope: defaults for --repo, bare issue numbers, the header, `where`.

// scopeRepos is the repos a command defaults to: the resolved scope, or every repo when none was resolved.
func (a *App) scopeRepos() []Alias {
	if a.Scope.Kind == "" {
		return a.Shorts()
	}
	return append([]Alias{}, a.Scope.Repos...)
}

// scoped is true when the scope was chosen on purpose (a project or -R), not the global default.
func (a *App) scoped() bool { return a.Scope.Kind == KindProject || a.Scope.Kind == KindExplicit }

// inScope reports whether alias is one of the repos a command defaults to.
func (a *App) inScope(alias string) bool {
	return a.Scope.Kind == "" || a.Scope.Kind == KindGlobal || contains(a.Scope.Repos, alias)
}

// multiRepo is true when a listing can mix repos, so it needs a REPO column.
func (a *App) multiRepo() bool { return len(a.scopeRepos()) > 1 }

// scopeNeedsOne is the single repo a write command needs (new): the scope's only repo, or a refusal that lists the choices.
func (a *App) scopeNeedsOne(what string) (string, error) {
	repos := a.scopeRepos()
	if len(repos) == 1 {
		return repos[0], nil
	}
	where := "you are not inside a project (scope: all repos)"
	if a.Scope.Kind == KindExplicit {
		where = "-R named several repos"
	}
	return "", die("%s needs one repo: %s. Pick one with -R <alias>; aliases: %s", what, where, strings.Join(a.Shorts(), ", "))
}

// takesRef: the command's first positional is an issue ref (the pos kinds in the command table are the one list of them).
func (sp *spec) takesRef() bool { return len(sp.pos) > 0 && sp.pos[0] == "ref" }

// bareRef turns a bare number ("585", "#585", "L-3") into alias#N using the scope. A ref that has an alias is left alone.
func (a *App) bareRef(ref string) (string, error) {
	id := strings.TrimPrefix(ref, "#")
	if strings.Contains(id, "#") || !validIdent(id) {
		return ref, nil
	}
	repos := a.scopeRepos()
	if len(repos) == 1 {
		return repos[0] + "#" + id, nil
	}
	var hit []string
	for _, r := range repos {
		if a.mirrorHas(r, id) {
			hit = append(hit, r)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0] + "#" + id, nil
	case 0:
		return "", die("no repo in scope has %s (%s); give it as <alias>#%s, aliases: %s", id, a.Scope.Label(), id, strings.Join(repos, ", "))
	}
	var opts []string
	for _, r := range hit {
		opts = append(opts, r+"#"+id)
	}
	return "", die("%s is ambiguous: it exists in %s; say which one (%s)", id, strings.Join(hit, ", "), strings.Join(opts, " or "))
}

// mirrorHas reports whether the mirror (or the outbox, for a local id) has this issue in this repo.
func (a *App) mirrorHas(short, ident string) bool {
	if isLocalIdent(ident) {
		for _, op := range a.loadOutbox() {
			if op.Op == "new" && op.Ref == short+"#"+ident {
				return true
			}
		}
		return a.localmapLookup(short, ident) != 0
	}
	for _, r := range a.readIndex(short) {
		if r[0] == ident {
			return true
		}
	}
	_, err := os.Stat(filepath.Join(a.Home, short, ident+".md"))
	return err == nil
}

// scopeHeader is the line a terminal gets above every list.
func (a *App) scopeHeader() string {
	return a.P(cDim, fmt.Sprintf("scope: %s (%s)", a.Scope.Label(), a.Scope.Reason))
}

// printScopeHeader writes the header to stderr (like the "showing N of M" footer) when a table is going to a terminal.
func (a *App) printScopeHeader(sp *spec, o *Opts) {
	if !a.TTY || a.Scope.Kind == "" || !sp.list {
		return
	}
	switch f := o.str("format"); {
	case o.B["json"], f == "json", f == "tsv":
		return
	}
	fmt.Fprintln(a.Err, a.scopeHeader())
}

// configSource says how the config file in use was found.
func configSource(explicit, cwd string, getenv func(string) string, cfg *Config) string {
	switch {
	case explicit != "":
		return "--config"
	case getenv("MOON_CONFIG") != "":
		return "$MOON_CONFIG"
	case within(realPath(configRoot(cfg.Path)), realPath(cwd)):
		return "found by walking up from the working directory"
	}
	return "user config"
}

// cmdWhere prints the resolved scope and every rule's result, so a wrong scope can be debugged.
func (a *App) cmdWhere(o *Opts) error {
	opt := ScopeOpts{Flags: o.L["repo"], All: o.B["all"], Home: a.Environ("HOME")}
	sc, rules := ExplainScope(a.Cwd, a.Cfg, a.GitRemote, opt)
	a.printf("scope:  %s\n", sc.Label())
	a.printf("kind:   %s\n", sc.Kind)
	a.printf("reason: %s\n", sc.Reason)
	a.printf("config: %s (%s)\n", a.Cfg.Path, a.ConfigSource)
	a.printf("cwd:    %s\n", realPath(a.Cwd))
	a.printf("rules, first match wins:\n")
	won := false
	for i, r := range rules {
		mark := "  "
		res := "no match"
		if len(r.Repos) > 0 {
			res = "matches " + strings.Join(r.Repos, ", ")
			if !won {
				mark, won = "=>", true
			}
		}
		a.printf(" %s %d. %-22s %s; %s\n", mark, i+1, r.Rule, res, r.Detail)
	}
	return nil
}
