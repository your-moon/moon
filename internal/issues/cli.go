package issues

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type fl struct {
	name    string
	short   string
	kind    byte // 's' string, 'b' bool, 'a' repeatable string
	choices []string
	def     string
	req     bool
}

type flset []fl

func (f flset) with(more ...[]fl) flset { return append(f, cat(more...)...) }

type spec struct {
	fls  []fl
	npos int
	read bool // read command: autosync runs first
	run  func(a *App, o *Opts) error
}

func repoFl() fl    { return fl{name: "repo", kind: 's'} }
func freshFl() fl   { return fl{name: "fresh", kind: 'b'} }
func bodyFls() []fl { return []fl{{name: "body", kind: 's'}, {name: "body-file", kind: 's'}} }

// emptyBodyFl lets new and edit accept a blank body on purpose; see readBodyGuarded.
func emptyBodyFl() fl { return fl{name: "allow-empty-body", kind: 'b'} }

// fmtFls: how a human wants the output. sortFls: ordering and size, for the long lists.
func fmtFls() []fl {
	return []fl{{name: "format", kind: 's', choices: []string{"auto", "table", "tsv", "json"}, def: "auto"}, {name: "no-color", kind: 'b'},
		{name: "color", kind: 's', choices: []string{"auto", "always", "never"}, def: "auto"}}
}
func sortFls() []fl {
	return []fl{{name: "sort", kind: 's', choices: []string{"number", "updated", "created", "title"}},
		{name: "reverse", short: "r", kind: 'b'}, {name: "limit", kind: 's'}}
}

// issueListFls are the flags of the commands that list issues: the status filters, sort by status, label display.
func issueListFls() []fl {
	return []fl{{name: "working", kind: 'b'}, {name: "idle", kind: 'b'}, {name: "status", kind: 's'}, {name: "show-status", kind: 'b'},
		{name: "labels", kind: 's', choices: []string{LabelsFull, LabelsShort, LabelsNone}},
		{name: "sort", kind: 's', choices: []string{"number", "updated", "created", "title", "status"}},
		{name: "reverse", short: "r", kind: 'b'}, {name: "limit", kind: 's'}}
}
func cat(l ...[]fl) []fl {
	var out []fl
	for _, x := range l {
		out = append(out, x...)
	}
	return out
}

func commands() map[string]*spec {
	one := func(f ...fl) flset { return f }
	m := map[string]*spec{
		"ls": {fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "closed", "all"}, def: "open"},
			fl{name: "label", kind: 's'}, fl{name: "milestone", kind: 's'}, fl{name: "q", short: "q", kind: 's'}).with(fmtFls(), issueListFls()), read: true, run: (*App).cmdLs},
		"show": {fls: one(freshFl(), fl{name: "show-status", kind: 'b'}).with(fmtFls()), npos: 1, read: true, run: (*App).cmdShow},
		"milestones": {fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "closed", "all"}}).with(fmtFls(),
			[]fl{{name: "sort", kind: 's', choices: []string{"due", "progress", "title", "open"}}, {name: "reverse", short: "r", kind: 'b'}, {name: "limit", kind: 's'}}),
			read: true, run: (*App).cmdMilestones},
		"milestones show": {fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "closed", "all"}, def: "open"},
			fl{name: "label", kind: 's'}).with(fmtFls(), issueListFls()), npos: 1, read: true, run: (*App).cmdMilestoneShow},
		"milestones new": {fls: one(repoFl(), fl{name: "due", kind: 's'}, fl{name: "description", kind: 's'}, fl{name: "description-file", kind: 's'}),
			npos: 1, run: (*App).cmdMsNew},
		"milestones edit": {fls: one(repoFl(), fl{name: "title", kind: 's'}, fl{name: "due", kind: 's'}, fl{name: "description", kind: 's'},
			fl{name: "description-file", kind: 's'}), npos: 1, run: (*App).cmdMsEdit},
		"labels new":        {fls: one(repoFl(), fl{name: "color", kind: 's'}, fl{name: "description", kind: 's'}), npos: 1, run: (*App).cmdLabelNew},
		"milestones close":  {fls: one(repoFl()), npos: 1, run: func(a *App, o *Opts) error { return a.cmdMsState("ms_close")(o) }},
		"milestones reopen": {fls: one(repoFl()), npos: 1, run: func(a *App, o *Opts) error { return a.cmdMsState("ms_reopen")(o) }},
		"prs": {fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "merged", "closed", "all"}, def: "open"},
			fl{name: "since", kind: 's'}, fl{name: "json", kind: 'b'}).with(fmtFls(), sortFls()), read: true, run: (*App).cmdPRs},
		"pr show":  {fls: one(freshFl()), npos: 1, read: true, run: (*App).cmdPRShow},
		"frontier": {fls: one(repoFl(), freshFl(), fl{name: "label", kind: 's'}, fl{name: "milestone", kind: 's'}).with(fmtFls(), issueListFls()), read: true, run: (*App).cmdFrontier},
		"claim": {fls: one(fl{name: "agent", kind: 's', req: true}, fl{name: "ttl", kind: 's'}, fl{name: "pid", kind: 's', def: "0"}),
			npos: 1, run: (*App).cmdClaim},
		"release": {fls: one(fl{name: "agent", kind: 's'}, fl{name: "force", kind: 'b'}), npos: 1, run: (*App).cmdRelease},
		"claims":  {fls: one(freshFl(), fl{name: "show-status", kind: 'b'}).with(fmtFls()), read: true, run: (*App).cmdClaims},
		"sweep":   {fls: one(freshFl(), fl{name: "release", kind: 'b'}, fl{name: "live", kind: 'b'}, fl{name: "agent", kind: 's'}).with(fmtFls()), read: true, run: (*App).cmdSweep},
		"status":  {fls: fmtFls(), run: (*App).cmdStatus},
		"new": {fls: cat(one(repoFl(), fl{name: "title", kind: 's', req: true}, fl{name: "label", kind: 'a'},
			fl{name: "milestone", kind: 's'}, fl{name: "now", kind: 'b'}, fl{name: "depends-on", kind: 'a'}, emptyBodyFl()), bodyFls()), run: (*App).cmdNew},
		"comment": {fls: bodyFls(), npos: 1, run: func(a *App, o *Opts) error { return a.cmdSimple("comment")(o) }},
		"close": {fls: one(fl{name: "reason", kind: 's', choices: []string{"completed", "not_planned"}, def: "completed"},
			fl{name: "comment", kind: 's'}, fl{name: "agent", kind: 's'}), npos: 1, run: func(a *App, o *Opts) error { return a.cmdSimple("close")(o) }},
		"reopen": {npos: 1, run: func(a *App, o *Opts) error { return a.cmdSimple("reopen")(o) }},
		"label": {fls: one(fl{name: "add", kind: 'a'}, fl{name: "remove", kind: 'a'}), npos: 1,
			run: func(a *App, o *Opts) error { return a.cmdSimple("label")(o) }},
		"milestone": {npos: 2, run: func(a *App, o *Opts) error { return a.cmdSimple("milestone")(o) }},
		"edit": {fls: cat(one(fl{name: "title", kind: 's'}, fl{name: "depends-on", kind: 'a'}, emptyBodyFl()), bodyFls()), npos: 1,
			run: func(a *App, o *Opts) error { return a.cmdSimple("edit")(o) }},
		"push":         {fls: one(repoFl()), run: (*App).cmdPush},
		"pull":         {fls: one(repoFl(), fl{name: "full", kind: 'b'}), run: (*App).cmdPull},
		"prune":        {fls: one(repoFl()), run: (*App).cmdPrune},
		"sync":         {fls: one(repoFl(), fl{name: "quiet", kind: 'b'}), run: (*App).cmdSync},
		"where":        {run: (*App).cmdWhere},
		"outbox ls":    {fls: fmtFls(), run: (*App).cmdOutboxLs},
		"outbox retry": {npos: 1, run: (*App).cmdOutboxRetry},
		"outbox drop":  {fls: one(fl{name: "cascade", kind: 'b'}), npos: 1, run: (*App).cmdOutboxDrop},
		"outbox edit": {fls: cat(one(fl{name: "title", kind: 's'}, fl{name: "label", kind: 'a'}, fl{name: "milestone", kind: 's'},
			fl{name: "add", kind: 'a'}, fl{name: "remove", kind: 'a'}, emptyBodyFl()), bodyFls()), npos: 1, run: (*App).cmdOutboxEdit},
	}
	// every command can be pointed at repos with -R (repeatable) and widened with --all; see scope.go
	for _, sp := range m {
		has := false
		for _, f := range sp.fls {
			has = has || f.name == "repo"
		}
		if !has {
			sp.fls = append(sp.fls, repoFl())
		}
		sp.fls = append(sp.fls, fl{name: "all", kind: 'b'})
	}
	return m
}

const usageText = `usage: moon issues [--config FILE] [--dir MIRROR] <command> [options]

commands:
  ls [--repo R] [--state open|closed|all] [--label L] [--milestone M] [-q text]
  show <ref>                     milestones [--repo R]
  milestones show <title>        milestones new|edit|close|reopen ...   (big work = milestone; see: moon issues help milestones new)
  prs [--repo R] [--state open|merged|closed|all] [--since ISO] [--json]
  pr show <ref>                  frontier [--repo R] [--label L] [--milestone M]
  claim <ref> --agent NAME [--ttl 120m] [--pid N]    (exit 3 = held by someone else)
  release <ref> (--agent NAME | --force)             claims    status
  sweep [--release]              list (or release) claims that are stale or on closed issues
  new --repo R --title T (--body B | --body-file F|-) [--label L ...] [--milestone M] [--now] [--allow-empty-body]
  comment|close|reopen|label|milestone|edit <ref> ...
  push [--repo R]   pull [--repo R] [--full]   sync [--repo R] [--quiet]   prune [--repo R]
  where                          which repos commands default to here, and why
  outbox [ls]                    list the queued writes (what sync will send), with their last error
  outbox edit <op> [--title T] [--body B | --body-file F|-] [--label L ...] [--milestone M] [--add L ...] [--remove L ...]
  outbox drop <op> [--cascade]   fix or remove a queued write that sync cannot send (<op> = a piece of the op id)

Reading commands (ls frontier prs claims status milestones show) also take:
  --format auto|table|tsv|json   auto = table on a terminal, tab-separated when piped
  --no-color                     (colour is also off when NO_COLOR is set or TERM=dumb)
  --color auto|always|never      always = colour a piped table (moon issues ls --color always | less -R)
  --sort number|updated|created|title  [--reverse]  --limit N     (ls, frontier, prs; limit 0 = all)
  --sort status                  (ls, frontier) stale, working, in review, blocked, ready, open, done

Work status (ls, frontier, show, claims): derived from the issue, its claim and the open pull requests.
  -w --working   only status working          -i --idle   only ready or open (nobody on it)
  --status a,b   only these (stale working in-review blocked ready open done)   --show-status  add it to piped output
  -B --labels full|short|none    how the LABELS column is shown; -L accepts the shown name or a unique suffix

Short flags (the same in every command that has the long one; they combine: -rl5, -s up, -l5, --limit=5):
  -s --sort      -l --limit      -r --reverse    -f --format (-j = json, -t = tsv)
  -L --label     -m --milestone  -S --state      -R --repo   -a --agent
  -q --query     -n --no-color   -h --help
  -w --working   -i --idle       -B --labels
  -d --due       -D --description                                       (milestones new, milestones edit)
  --sort values may be abbreviated (-s up = updated); "moon issues ls 10" shows 10 rows.

moon issues <command> --help shows the options and two examples for that command.
Projects and scope (moon issues where shows what applies here and why). Commands default to one repo when you are in a project:
  1. -R/--repo ALIAS (repeatable) names the repos; -R all or --all means every repo
  2. a moon.toml in this folder or a parent that declares exactly one repo
  3. paths = ["~/work/app"] under [repos.<alias>] (longest path wins; covers folders that are not git repos)
  4. the git remote "origin", matched to a repo's github = "owner/name" (case-insensitive)
  5. otherwise every repo. A bare number (585, #585) means that repo; in all-repos mode it must exist in exactly one.
Refs: <alias>#123, or <alias>#L-<id> for a local issue not pushed yet.
Aliases come from [repos.<alias>] in moon.toml. Env: ISSUES_NO_AUTOSYNC, ISSUES_KEEP_DAYS, ISSUES_GH, NO_COLOR.
`

// shortMap is the one set of short flags, the same in every command that has the flag. -j and -t are
// shortcuts for --format json / --format tsv. A command that does not have the long flag does not get the short one.
var shortMap = map[string]string{"s": "sort", "l": "limit", "r": "reverse", "f": "format", "L": "label", "m": "milestone",
	"S": "state", "R": "repo", "a": "agent", "n": "no-color", "w": "working", "i": "idle", "B": "labels", "d": "due", "D": "description"}

// shortKey is the single letter that selects f in this command ("" if none).
func (f fl) shortKey() string {
	if f.short != "" {
		return f.short
	}
	for k, v := range shortMap {
		if v == f.name {
			return k
		}
	}
	return ""
}

func uniquePrefix(val string, choices []string) (string, bool) {
	hit := ""
	for _, c := range choices {
		if strings.HasPrefix(c, val) {
			if hit != "" {
				return "", false
			}
			hit = c
		}
	}
	return hit, hit != "" && val != ""
}

func shortHint(c string, byName map[string]*fl) string {
	if sw := strings.ToLower(c); sw != c && byName["-"+sw] != nil {
		return fmt.Sprintf(", did you mean -%s (--%s)?", sw, byName["-"+sw].name)
	}
	if sw := strings.ToUpper(c); sw != c && byName["-"+sw] != nil {
		return fmt.Sprintf(", did you mean -%s (--%s)?", sw, byName["-"+sw].name)
	}
	var names []string
	for k, f := range byName {
		if len(k) == 2 && k[1] != 'h' {
			if strings.HasPrefix(f.name, strings.ToLower(c)) {
				return fmt.Sprintf(", did you mean %s (--%s)?", k, f.name)
			}
			names = append(names, k)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return ", short flags here: " + strings.Join(names, " ")
}

func parseOpts(sp *spec, name string, args []string, aliases []string) (*Opts, error) {
	o := &Opts{S: map[string]string{}, L: map[string][]string{}, B: map[string]bool{}, Seen: map[string]bool{}}
	byName := map[string]*fl{}
	for i := range sp.fls {
		f := &sp.fls[i]
		byName["--"+f.name] = f
		if k := f.shortKey(); k != "" {
			byName["-"+k] = f
		}
		if f.name == "q" {
			byName["--query"] = f
		}
		if f.def != "" {
			o.S[f.name] = f.def
		}
	}
	bad := func(format string, a ...any) error {
		return die("usage: moon issues %s [options]\nmoon issues %s: error: %s", name, name, fmt.Sprintf(format, a...))
	}
	// set stores one value for f, applying choices (unique prefixes allowed) and the --limit check.
	set := func(f *fl, val string) error {
		choices := f.choices
		if f.name == "repo" {
			choices = append(append([]string{}, aliases...), "all")
		} else if len(choices) > 0 && !contains(choices, val) {
			if full, ok := uniquePrefix(val, choices); ok {
				val = full
			}
		}
		if len(choices) > 0 && !contains(choices, val) {
			return bad("argument --%s: invalid choice: '%s' (choose from %s)", f.name, val, strings.Join(choices, ", "))
		}
		if f.name == "limit" {
			if n, err := strconv.Atoi(val); err != nil || n < 0 {
				return bad("argument --limit: expected a number >= 0 (0 shows everything), got '%s'", val)
			}
		}
		o.Seen[f.name] = true
		if f.name == "repo" { // -R is repeatable: the scope takes them all
			o.L["repo"] = append(o.L["repo"], val)
		}
		if f.kind == 'a' {
			o.L[f.name] = append(o.L[f.name], val)
		} else {
			o.S[f.name] = val
		}
		return nil
	}
	formatFl := byName["--format"]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			o.Pos = append(o.Pos, arg)
			continue
		}
		if !strings.HasPrefix(arg, "--") { // short flags, possibly combined: -rl5  -s up  -l=5
			cl := arg[1:]
			for j := 0; j < len(cl); {
				c := cl[j : j+1]
				j++
				if (c == "j" || c == "t") && formatFl != nil {
					if err := set(formatFl, map[string]string{"j": "json", "t": "tsv"}[c]); err != nil {
						return nil, err
					}
					continue
				}
				f := byName["-"+c]
				if f == nil {
					return nil, bad("unknown flag -%s%s; see moon issues %s --help", c, shortHint(c, byName), name)
				}
				if f.kind == 'b' {
					o.B[f.name] = true
					o.Seen[f.name] = true
					continue
				}
				val := strings.TrimPrefix(cl[j:], "=")
				if cl[j:] == "" {
					if i+1 >= len(args) {
						return nil, bad("argument -%s (--%s): expected one argument", c, f.name)
					}
					i++
					val = args[i]
				}
				if err := set(f, val); err != nil {
					return nil, err
				}
				break
			}
			continue
		}
		key, val, hasVal := strings.Cut(arg, "=")
		f := byName[key]
		if f == nil {
			var names []string
			for k := range byName {
				names = append(names, k)
			}
			hint := ""
			if c := closest(key, names); c != "" {
				hint = " (did you mean " + c + "?)"
			}
			return nil, bad("unrecognized arguments: %s%s; see moon issues %s --help", arg, hint, name)
		}
		if f.kind == 'b' {
			o.B[f.name] = true
			o.Seen[f.name] = true
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return nil, bad("argument --%s: expected one argument", f.name)
			}
			i++
			val = args[i]
		}
		if err := set(f, val); err != nil {
			return nil, err
		}
	}
	// `moon issues ls 10`: a lone number is the limit
	if lf := byName["--limit"]; lf != nil && sp.npos == 0 && len(o.Pos) == 1 && !o.Seen["limit"] {
		if _, err := strconv.Atoi(o.Pos[0]); err == nil && o.Pos[0][0] != '-' {
			if err := set(lf, o.Pos[0]); err != nil {
				return nil, err
			}
			o.Pos = nil
		}
	}
	for _, f := range sp.fls {
		if f.req && !o.Seen[f.name] {
			return nil, bad("the following arguments are required: --%s", f.name)
		}
	}
	if len(o.Pos) != sp.npos {
		return nil, bad("expected %d positional argument(s), got %d", sp.npos, len(o.Pos))
	}
	return o, nil
}

// Env is everything Main needs from the process.
type Env struct {
	Getenv func(string) string
	Stdin  io.Reader
	Out    io.Writer
	Err    io.Writer
	Cwd    string
	GH     GitHub // nil: the real gh
	// GitRemote returns the origin URL of the checkout around a directory (project scope); nil: run git.
	GitRemote GitRemoteFunc
	Now       func() time.Time
	// TTY is true when Out is a terminal; Width is its column count (0 = unknown, 100 is used).
	TTY   bool
	Width int
}

// Main runs `moon issues <args>` and returns the exit status.
func Main(args []string, e Env) int {
	if e.Now == nil {
		e.Now = time.Now
	}
	var cfgPath, dir string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch k, v, hv := strings.Cut(args[i], "="); k {
		case "--config", "--dir":
			if !hv {
				if i+1 >= len(args) {
					fmt.Fprintf(e.Err, "%s needs a value\n", k)
					return 2
				}
				i++
				v = args[i]
			}
			if k == "--config" {
				cfgPath = v
			} else {
				dir = v
			}
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) >= 2 && rest[0] == "help" { // moon issues help ls
		name := rest[1]
		if name == "pr" {
			name = "pr show"
		}
		if name == "labels" {
			name = "labels new"
		}
		if name == "milestones" && len(rest) > 2 && isMsSub(rest[2]) {
			name = "milestones " + rest[2]
		}
		if name == "labels" && len(rest) > 2 && isLabelSub(rest[2]) {
			name = "labels " + rest[2]
		}
		if name == "outbox" {
			name = "outbox ls"
			if len(rest) > 2 && isOutboxSub(rest[2]) {
				name = "outbox " + rest[2]
			}
		}
		if _, ok := helpTexts[name]; !ok {
			fmt.Fprint(e.Err, unknownCommand(rest[1]))
			return 2
		}
		fmt.Fprint(e.Out, cmdHelp(name))
		return 0
	}
	if len(rest) == 0 || rest[0] == "-h" || rest[0] == "--help" || rest[0] == "help" {
		fmt.Fprint(e.Out, usageText)
		if len(rest) == 0 {
			return 2
		}
		return 0
	}
	name := rest[0]
	rest = rest[1:]
	if name == "milestones" && len(rest) > 0 && isMsSub(rest[0]) {
		name, rest = "milestones "+rest[0], rest[1:]
	}
	if name == "labels" && len(rest) > 0 && isLabelSub(rest[0]) {
		name, rest = "labels "+rest[0], rest[1:]
	}
	if name == "outbox" {
		sub := "ls" // bare `outbox`, or only flags, lists
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			sub = rest[0]
			rest = rest[1:]
		}
		if !isOutboxSub(sub) {
			fmt.Fprintf(e.Err, "moon issues outbox: unknown subcommand %q (ls, edit, drop)\n", sub)
			return 2
		}
		name = "outbox " + sub
	}
	if name == "pr" {
		if len(rest) > 0 && (rest[0] == "-h" || rest[0] == "--help") {
			fmt.Fprint(e.Out, cmdHelp("pr show"))
			return 0
		}
		if len(rest) == 0 || rest[0] != "show" {
			fmt.Fprint(e.Err, "usage: moon issues pr show <ref>   (see moon issues pr show --help)\n")
			return 2
		}
		name, rest = "pr show", rest[1:]
	}
	sp := commands()[name]
	if sp == nil {
		fmt.Fprint(e.Err, unknownCommand(name))
		return 2
	}
	if helpFlagSeen(sp, rest) {
		fmt.Fprint(e.Out, cmdHelp(name))
		return 0
	}
	p, err := FindConfig(cfgPath, e.Cwd, e.Getenv)
	if err != nil {
		fmt.Fprintln(e.Err, err)
		return 2
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		fmt.Fprintln(e.Err, err)
		return 2
	}
	home := cfg.MirrorDir
	if dir != "" {
		home, _ = filepath.Abs(dir)
	}
	host, _ := os.Hostname()
	gh := e.GH
	if gh == nil {
		gh = ExecGH{Bin: e.Getenv("ISSUES_GH")}
	}
	git := e.GitRemote
	if git == nil {
		git = GitRemoteOrigin
	}
	a := &App{Cfg: cfg, Cwd: e.Cwd, GitRemote: git, ConfigSource: configSource(cfgPath, e.Cwd, e.Getenv, cfg), Home: home, GH: gh, Out: e.Out, Err: e.Err, Stdin: e.Stdin, Now: e.Now, Host: host, Environ: e.Getenv,
		Repos: cfg.Repos, SpawnSync: spawnSync, TTY: e.TTY, Width: e.Width}
	if a.Width <= 0 {
		a.Width = defaultWidth
	}
	o, err := parseOpts(sp, name, rest, a.Shorts())
	if err != nil {
		fmt.Fprintln(e.Err, err)
		return 2
	}
	if e.Cwd != "" {
		a.Scope = ResolveScope(e.Cwd, cfg, git, ScopeOpts{Flags: o.L["repo"], All: o.B["all"], Home: e.Getenv("HOME")})
	}
	delete(o.S, "repo") // from here on the scope is the only source of the repo filter
	if refCommands[name] {
		ref, err := a.bareRef(o.Pos[0])
		if err != nil {
			fmt.Fprintln(e.Err, err)
			return 2
		}
		o.Pos[0] = ref
	}
	caps := DetectColor(e.Getenv, e.TTY, o.B["no-color"], o.str("color"))
	a.Color, a.Forced = caps.On, caps.Forced
	a.St = NewStyler(caps, e.Getenv, cfg.Colors)
	if sp.read {
		if err := a.autosync(name, o, cfgPath); err != nil {
			fmt.Fprintln(e.Err, err)
			return 2
		}
	}
	a.printScopeHeader(name, o)
	if err := sp.run(a, o); err != nil {
		if ee, ok := err.(*exitError); ok {
			if ee.Msg != "" {
				fmt.Fprintln(e.Err, ee.Msg)
			}
			return ee.Code
		}
		fmt.Fprintln(e.Err, err)
		return 1
	}
	return 0
}

// ---------- auto-refresh ----------

func (a *App) mirrorAge(shorts []string) (float64, bool) {
	var max float64
	for _, s := range shorts {
		b, err := readText(filepath.Join(a.Home, s, "lastpull"))
		t, perr := strconv.Atoi(pyStrip(b))
		if err != nil || perr != nil {
			return 0, false
		}
		if age := float64(a.Now().UnixNano())/1e9 - float64(t); age > max {
			max = age
		}
	}
	return max, true
}

func (a *App) takeAutosyncMarker() bool {
	os.MkdirAll(a.Home, 0o755)
	path := filepath.Join(a.Home, ".autosync")
	for i := 0; i < 2; i++ {
		if err := createExcl(path, strconv.Itoa(os.Getpid())+"\n"); err == nil { // M_asmarker
			return true
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if time.Since(st.ModTime()).Seconds() <= autosyncMarkerStale { // M_asstale
			return false
		}
		gone := path + ".stale." + uuid4()
		if os.Rename(path, gone) == nil {
			os.Remove(gone)
		}
	}
	return false
}

func (a *App) autosync(name string, o *Opts, cfgPath string) error {
	shorts := a.shortsFor(o)
	if name == "show" || name == "pr show" {
		s, _, err := a.parseRef(o.Pos[0])
		if err != nil {
			return err
		}
		shorts = []string{s}
	}
	shorts, _ = a.splitLocal(shorts) // local-only aliases have nothing to refresh
	if len(shorts) == 0 {
		return nil
	}
	if o.B["fresh"] { // M_fresh
		a.doSync(shorts, false, a.Err, nil)
		return nil
	}
	if a.Environ("ISSUES_NO_AUTOSYNC") != "" {
		return nil
	}
	age, ok := a.mirrorAge(shorts)
	if ok && age <= autosyncMaxAge { // M_asage
		return nil
	}
	if !a.takeAutosyncMarker() {
		return nil
	}
	if err := a.SpawnSync(a); err != nil {
		os.Remove(filepath.Join(a.Home, ".autosync"))
		return nil
	}
	if ok {
		fmt.Fprintf(a.Err, "(mirror %d min old; refreshing in background)\n", int(age)/60)
	} else {
		fmt.Fprintf(a.Err, "(mirror never pulled, so old; refreshing in background)\n")
	}
	return nil
}

// spawnSync starts `moon issues sync --quiet` fully detached, output appended to autosync.log.
func spawnSync(a *App) error {
	if f := a.Environ("ISSUES_TEST_SPAWN"); f != "" {
		fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		fh.WriteString("spawn\n")
		return fh.Close()
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(a.Home, "autosync.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(self, "issues", "--dir", a.Home, "sync", "--quiet", "--all")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "ISSUES_NO_AUTOSYNC=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// isOutboxSub: the words that follow `moon issues outbox`.
func isOutboxSub(w string) bool { return w == "ls" || w == "edit" || w == "drop" || w == "retry" }

// isMsSub: the words that follow `moon issues milestones`.
func isMsSub(w string) bool {
	switch w {
	case "show", "new", "edit", "close", "reopen":
		return true
	}
	return false
}
