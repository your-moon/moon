package completion

import (
	"sort"
	"strings"

	"github.com/your-moon/moon/internal/issues"
)

// Candidate is one thing the user may type.
type Candidate struct {
	Value string
	Desc  string
}

// Directive tells the shell what to do besides offering the candidates.
type Directive int

const (
	// DirDefault lets the shell add file names when there are no candidates.
	DirDefault Directive = 0
	// DirNoFiles stops the shell from falling back to file names.
	DirNoFiles Directive = 4
)

// Result is the answer to one completion request.
type Result struct {
	Cands []Candidate
	Dir   Directive
}

// Command is one top-level command of the moon binary.
type Command struct{ Name, Summary string }

// Options is what the engine needs from the binary.
type Options struct {
	Top  []Command // top-level commands, in the order they are listed
	Open func(config, dir string) Source
}

// MaxRefs caps the number of ref candidates.
const MaxRefs = 200

var shells = []Candidate{{"zsh", "completion script for zsh"}, {"bash", "completion script for bash"}, {"fish", "completion script for fish"}}

var tuiFlags = []issues.FlagInfo{
	{Name: "config", Takes: true, Kind: "file", Desc: "path to moon.toml"},
	{Name: "dir", Takes: true, Kind: "file", Desc: "mirror directory"},
	{Name: "agent", Takes: true, Kind: "text", Desc: "claim holder name (default $USER)"},
}

// Complete answers `moon __complete <words...>`. words are the command line words after "moon"; the last
// one is the word under the cursor and may be empty.
func Complete(o Options, words []string) Result {
	if len(words) == 0 {
		words = []string{""}
	}
	cur, prev := words[len(words)-1], words[:len(words)-1]
	if len(prev) == 0 {
		var c []Candidate
		for _, t := range o.Top {
			c = append(c, Candidate{t.Name, t.Summary})
		}
		return Result{filterPrefix(c, cur, false), DirNoFiles}
	}
	src := newLazy(o, words)
	switch prev[0] {
	case "issues":
		return completeIssues(src, prev[1:], cur)
	case "tui":
		return walk(src, &issues.CmdInfo{Name: "tui", Flags: tuiFlags}, prev[1:], cur)
	case "completion":
		if len(prev) == 1 {
			return Result{filterPrefix(shells, cur, false), DirNoFiles}
		}
	case "help":
		if len(prev) == 1 {
			return Result{filterPrefix([]Candidate{{"issues", "help for the issues tool"}}, cur, false), DirNoFiles}
		}
	}
	return Result{Dir: DirNoFiles}
}

// lazy opens the mirror on first use, with the --config and --dir given anywhere on the line.
type lazy struct {
	o      Options
	cfg    string
	dir    string
	opened bool
	src    Source
}

func newLazy(o Options, words []string) *lazy {
	l := &lazy{o: o}
	for i := 0; i < len(words); i++ {
		k, v, hv := strings.Cut(words[i], "=")
		if k != "--config" && k != "--dir" {
			continue
		}
		if !hv {
			if i+1 >= len(words)-1 { // the value is still being typed
				continue
			}
			i++
			v = words[i]
		}
		if k == "--config" {
			l.cfg = v
		} else {
			l.dir = v
		}
	}
	return l
}

func (l *lazy) get() Source {
	if !l.opened {
		l.opened = true
		if l.o.Open != nil {
			l.src = l.o.Open(l.cfg, l.dir)
		}
	}
	return l.src
}

func globalFlagInfo() *issues.CmdInfo {
	return &issues.CmdInfo{Name: "issues", Flags: append(issues.GlobalFlags(), issues.FlagInfo{Name: "help", Short: "h", Desc: "show the commands"})}
}

func completeIssues(src *lazy, prev []string, cur string) Result {
	gl := globalFlagInfo()
	// leading --config/--dir
	i := 0
	for i < len(prev) && strings.HasPrefix(prev[i], "-") {
		k, _, hv := strings.Cut(prev[i], "=")
		if (k == "--config" || k == "--dir") && !hv {
			i++
		}
		i++
	}
	if i > len(prev) { // the last global flag still wants its value
		return walk(src, gl, prev, cur)
	}
	if i == len(prev) { // no command yet
		if strings.HasPrefix(cur, "-") {
			return walk(src, gl, prev, cur)
		}
		return Result{filterPrefix(issuesCommands(), cur, false), DirNoFiles}
	}
	name, rest := prev[i], prev[i+1:]
	switch name {
	case "help":
		if len(rest) == 0 {
			return Result{filterPrefix(issuesCommands(), cur, false), DirNoFiles}
		}
		return Result{Dir: DirNoFiles}
	case "tui":
		return walk(src, &issues.CmdInfo{Name: "tui", Flags: tuiFlags}, rest, cur)
	}
	all := issues.Describe()
	find := func(n string) *issues.CmdInfo {
		for k := range all {
			if all[k].Name == n {
				return &all[k]
			}
		}
		return nil
	}
	switch name {
	case "pr":
		if len(rest) == 0 {
			return Result{filterPrefix([]Candidate{{"show", "print one pull request"}}, cur, false), DirNoFiles}
		}
		if rest[0] == "show" {
			return walkCmd(src, find("pr show"), rest[1:], cur)
		}
		return Result{Dir: DirNoFiles}
	case "milestones":
		if len(rest) > 0 {
			if ci := find("milestones " + rest[0]); ci != nil {
				return walkCmd(src, ci, rest[1:], cur)
			}
		}
	}
	ci := find(name)
	if ci == nil {
		return Result{Dir: DirNoFiles}
	}
	return walkCmd(src, ci, rest, cur)
}

func walkCmd(src *lazy, ci *issues.CmdInfo, rest []string, cur string) Result {
	if ci == nil {
		return Result{Dir: DirNoFiles}
	}
	r := walk(src, ci, rest, cur)
	if ci.Name == "milestones" && !strings.HasPrefix(cur, "-") && len(rest) == 0 {
		var subs []Candidate
		for _, n := range []string{"show", "new", "edit", "close", "reopen"} {
			subs = append(subs, Candidate{n, helpSummary("milestones " + n)})
		}
		r.Cands = append(filterPrefix(subs, cur, false), r.Cands...)
	}
	return r
}

func issuesCommands() []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, ci := range issues.Describe() {
		n, _, _ := strings.Cut(ci.Name, " ")
		if seen[n] {
			continue
		}
		seen[n] = true
		sum := ci.Summary
		if n == "pr" {
			sum = "pull request commands (pr show)"
		}
		out = append(out, Candidate{n, sum})
	}
	out = append(out, Candidate{"tui", "interactive terminal dashboard"}, Candidate{"help", "print the usage text"})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

func findFlag(ci *issues.CmdInfo, long string, short byte) *issues.FlagInfo {
	for i := range ci.Flags {
		f := &ci.Flags[i]
		if long != "" {
			if f.Name == long {
				return f
			}
			for _, a := range f.Aliases {
				if a == long {
					return f
				}
			}
		} else if f.Short != "" && f.Short[0] == short {
			return f
		}
	}
	return nil
}

// walk understands the words before the cursor (flags, their values, positionals) and completes the last word.
func walk(src *lazy, ci *issues.CmdInfo, prev []string, cur string) Result {
	used := map[string]bool{}
	var pending *issues.FlagInfo
	npos := 0
	var repos []string
	allRepos := false // -R all or --all
	note := func(f *issues.FlagInfo, val string) {
		if f.Name == "repo" && val == "all" {
			allRepos = true
		} else if f.Name == "repo" && val != "" {
			repos = append(repos, val)
		}
	}
	// curRepos is the repos the line is about: the -R values, else the project scope, else all (nil)
	curRepos := func(s Source) []string {
		if allRepos {
			return nil
		}
		if len(repos) > 0 {
			return repos
		}
		if sc, ok := s.(scoped); ok {
			return sc.Scope()
		}
		return nil
	}
	gl := globalFlagInfo()
	lookup := func(long string, short byte) *issues.FlagInfo {
		if f := findFlag(ci, long, short); f != nil {
			return f
		}
		if ci.Name != "issues" && ci.Name != "tui" {
			return findFlag(gl, long, short)
		}
		return nil
	}
	for _, w := range prev {
		switch {
		case pending != nil:
			note(pending, w)
			pending = nil
		case w == "--":
		case strings.HasPrefix(w, "--"):
			k, v, hv := strings.Cut(w[2:], "=")
			if f := lookup(k, 0); f != nil {
				used[flagKey(f)] = true
				if f.Name == "all" {
					allRepos = true
				}
				if f.Takes && !hv {
					pending = f
				} else if hv {
					note(f, v)
				}
			}
		case strings.HasPrefix(w, "-") && len(w) > 1:
			for j := 1; j < len(w); j++ {
				f := lookup("", w[j])
				if f == nil {
					break
				}
				used[flagKey(f)] = true
				if !f.Takes {
					continue
				}
				if rest := strings.TrimPrefix(w[j+1:], "="); rest != "" {
					note(f, rest)
				} else {
					pending = f
				}
				break
			}
		default:
			npos++
		}
	}
	flagCands := func(long, short bool) []Candidate {
		var out []Candidate
		list := ci.Flags
		if ci.Name != "issues" && ci.Name != "tui" {
			list = append(append([]issues.FlagInfo{}, ci.Flags...), gl.Flags[:2]...)
		}
		for _, f := range list {
			if used[flagKey(&f)] && !f.Repeat {
				continue
			}
			if long && f.Name != "" {
				out = append(out, Candidate{"--" + f.Name, f.Desc})
			}
			if short && f.Short != "" {
				out = append(out, Candidate{"-" + f.Short, firstNonEmpty(f.Desc, "--"+f.Name)})
			}
		}
		return out
	}
	values := func(f *issues.FlagInfo, prefix, typed string) Result {
		var c []Candidate
		switch f.Kind {
		case "enum":
			for _, v := range f.Choices {
				c = append(c, Candidate{v, ""})
			}
		case "repo":
			if s := src.get(); s != nil {
				for _, a := range s.Aliases() {
					c = append(c, Candidate{a, "repo alias"})
				}
				c = append(c, Candidate{"all", "every repo, not only this project"})
			}
		case "label":
			if s := src.get(); s != nil {
				c = Labels(s, curRepos(s))
			}
		case "milestone":
			if s := src.get(); s != nil {
				c = Milestones(s, curRepos(s))
			}
		case "status": // a comma-separated list: complete the last name
			head := ""
			if i := strings.LastIndexByte(typed, ','); i >= 0 {
				head, typed = typed[:i+1], typed[i+1:]
			}
			for _, v := range issues.StatusOrder {
				c = append(c, Candidate{head + strings.ReplaceAll(v, " ", "-"), ""})
			}
			c = filterPrefix(c, head+typed, true)
			for i := range c {
				c[i].Value = prefix + c[i].Value
			}
			return Result{c, DirNoFiles}
		case "date":
			if ci.Name == "milestones edit" {
				c = append(c, Candidate{"none", "clear the due date"})
			}
		case "file":
			return Result{Dir: DirDefault}
		default:
			return Result{Dir: DirNoFiles}
		}
		c = filterPrefix(c, typed, f.Kind != "enum")
		for i := range c {
			c[i].Value = prefix + c[i].Value
		}
		return Result{c, DirNoFiles}
	}
	switch {
	case pending != nil:
		return values(pending, "", cur)
	case strings.HasPrefix(cur, "--"):
		if k, v, ok := strings.Cut(cur[2:], "="); ok {
			if f := lookup(k, 0); f != nil && f.Takes {
				return values(f, "--"+k+"=", v)
			}
			return Result{Dir: DirNoFiles}
		}
		return Result{filterPrefix(flagCands(true, false), cur, false), DirNoFiles}
	case cur == "-":
		return Result{filterPrefix(flagCands(true, true), cur, false), DirNoFiles}
	case strings.HasPrefix(cur, "-"):
		group := map[byte]bool{}
		for j := 1; j < len(cur); j++ {
			f := lookup("", cur[j])
			if f == nil {
				return Result{Dir: DirNoFiles}
			}
			if f.Takes {
				if j == len(cur)-1 {
					return values(f, cur, "")
				}
				return Result{Dir: DirNoFiles} // value typed inline: -l5
			}
			group[cur[j]] = true
		}
		var out []Candidate
		for _, c := range flagCands(false, true) {
			if !group[c.Value[1]] {
				out = append(out, Candidate{cur + c.Value[1:], c.Desc})
			}
		}
		return Result{out, DirNoFiles}
	}
	// a positional argument
	var kind string
	if npos < len(ci.Pos) {
		kind = ci.Pos[npos]
	}
	s := src.get()
	if s == nil {
		return Result{Dir: DirNoFiles}
	}
	switch kind {
	case "ref":
		rs := curRepos(s)
		if len(rs) == 1 { // one repo in scope: a bare number (585, #585) is a ref too
			if num := strings.TrimPrefix(cur, "#"); num != "" && strings.Trim(num, "0123456789") == "" {
				var out []Candidate
				for _, c := range Refs(s, ci.Name, rs[0]+"#"+num, rs) {
					out = append(out, Candidate{cur[:len(cur)-len(num)] + strings.TrimPrefix(c.Value, rs[0]+"#"), c.Desc})
				}
				return Result{out, DirNoFiles}
			}
		}
		return Result{Refs(s, ci.Name, cur, rs), DirNoFiles}
	case "milestone":
		state := map[string]string{"milestones close": "open", "milestones reopen": "closed"}[ci.Name]
		c := MilestonesFor(s, curRepos(s), state, true)
		if ci.Name == "milestone" {
			c = append([]Candidate{{"none", "clear the milestone"}}, c...)
		}
		return Result{filterPrefix(c, cur, true), DirNoFiles}
	}
	return Result{Dir: DirNoFiles}
}

func flagKey(f *issues.FlagInfo) string {
	if f.Name != "" {
		return f.Name
	}
	return f.Short
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// filterPrefix keeps the candidates that start with typed; fold ignores case.
func filterPrefix(c []Candidate, typed string, fold bool) []Candidate {
	if typed == "" {
		return c
	}
	var out []Candidate
	t := strings.ToLower(typed)
	for _, x := range c {
		if strings.HasPrefix(x.Value, typed) || fold && strings.HasPrefix(strings.ToLower(x.Value), t) {
			out = append(out, x)
		}
	}
	return out
}

func helpSummary(name string) string {
	for _, ci := range issues.Describe() {
		if ci.Name == name {
			return ci.Summary
		}
	}
	return ""
}
