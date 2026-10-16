package issues

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

type spec struct {
	fls  []fl
	npos int
	read bool // read command: autosync runs first
	run  func(a *App, o *Opts) error
}

func repoFl() fl    { return fl{name: "repo", kind: 's'} }
func freshFl() fl   { return fl{name: "fresh", kind: 'b'} }
func bodyFls() []fl { return []fl{{name: "body", kind: 's'}, {name: "body-file", kind: 's'}} }
func cat(l ...[]fl) []fl {
	var out []fl
	for _, x := range l {
		out = append(out, x...)
	}
	return out
}

func commands() map[string]*spec {
	one := func(f ...fl) []fl { return f }
	return map[string]*spec{
		"ls": {fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "closed", "all"}, def: "open"},
			fl{name: "label", kind: 's'}, fl{name: "milestone", kind: 's'}, fl{name: "q", short: "q", kind: 's'}), read: true, run: (*App).cmdLs},
		"show":       {fls: one(freshFl()), npos: 1, read: true, run: (*App).cmdShow},
		"milestones": {fls: one(repoFl(), freshFl()), read: true, run: (*App).cmdMilestones},
		"prs": {fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "merged", "closed", "all"}, def: "open"},
			fl{name: "since", kind: 's'}, fl{name: "json", kind: 'b'}), read: true, run: (*App).cmdPRs},
		"pr show":  {fls: one(freshFl()), npos: 1, read: true, run: (*App).cmdPRShow},
		"frontier": {fls: one(repoFl(), freshFl(), fl{name: "label", kind: 's'}, fl{name: "milestone", kind: 's'}), read: true, run: (*App).cmdFrontier},
		"claim": {fls: one(fl{name: "agent", kind: 's', req: true}, fl{name: "ttl", kind: 's', def: "120m"}, fl{name: "pid", kind: 's', def: "0"}),
			npos: 1, run: (*App).cmdClaim},
		"release": {fls: one(fl{name: "agent", kind: 's'}, fl{name: "force", kind: 'b'}), npos: 1, run: (*App).cmdRelease},
		"claims":  {fls: one(freshFl()), read: true, run: (*App).cmdClaims},
		"status":  {run: (*App).cmdStatus},
		"new": {fls: cat(one(fl{name: "repo", kind: 's', req: true}, fl{name: "title", kind: 's', req: true}, fl{name: "label", kind: 'a'},
			fl{name: "milestone", kind: 's'}, fl{name: "now", kind: 'b'}), bodyFls()), run: (*App).cmdNew},
		"comment": {fls: bodyFls(), npos: 1, run: func(a *App, o *Opts) error { return a.cmdSimple("comment")(o) }},
		"close": {fls: one(fl{name: "reason", kind: 's', choices: []string{"completed", "not_planned"}, def: "completed"},
			fl{name: "comment", kind: 's'}, fl{name: "agent", kind: 's'}), npos: 1, run: func(a *App, o *Opts) error { return a.cmdSimple("close")(o) }},
		"reopen": {npos: 1, run: func(a *App, o *Opts) error { return a.cmdSimple("reopen")(o) }},
		"label": {fls: one(fl{name: "add", kind: 'a'}, fl{name: "remove", kind: 'a'}), npos: 1,
			run: func(a *App, o *Opts) error { return a.cmdSimple("label")(o) }},
		"milestone": {npos: 2, run: func(a *App, o *Opts) error { return a.cmdSimple("milestone")(o) }},
		"edit": {fls: cat(one(fl{name: "title", kind: 's'}), bodyFls()), npos: 1,
			run: func(a *App, o *Opts) error { return a.cmdSimple("edit")(o) }},
		"push":  {fls: one(repoFl()), run: (*App).cmdPush},
		"pull":  {fls: one(repoFl(), fl{name: "full", kind: 'b'}), run: (*App).cmdPull},
		"prune": {fls: one(repoFl()), run: (*App).cmdPrune},
		"sync":  {fls: one(repoFl(), fl{name: "quiet", kind: 'b'}), run: (*App).cmdSync},
	}
}

const usageText = `usage: moon issues [--config FILE] [--dir MIRROR] <command> [options]

commands:
  ls [--repo R] [--state open|closed|all] [--label L] [--milestone M] [-q text]
  show <ref>                     milestones [--repo R]
  prs [--repo R] [--state open|merged|closed|all] [--since ISO] [--json]
  pr show <ref>                  frontier [--repo R] [--label L] [--milestone M]
  claim <ref> --agent NAME [--ttl 120m] [--pid N]    (exit 3 = held by someone else)
  release <ref> (--agent NAME | --force)             claims    status
  new --repo R --title T (--body B | --body-file F|-) [--label L ...] [--milestone M] [--now]
  comment|close|reopen|label|milestone|edit <ref> ...
  push [--repo R]   pull [--repo R] [--full]   sync [--repo R] [--quiet]   prune [--repo R]

Refs: <alias>#123, or <alias>#L-<id> for a local issue not pushed yet.
Aliases come from [repos.<alias>] in moon.toml. Env: ISSUES_NO_AUTOSYNC, ISSUES_KEEP_DAYS, ISSUES_GH.
`

func parseOpts(sp *spec, name string, args []string, aliases []string) (*Opts, error) {
	o := &Opts{S: map[string]string{}, L: map[string][]string{}, B: map[string]bool{}, Seen: map[string]bool{}}
	byName := map[string]*fl{}
	for i := range sp.fls {
		f := &sp.fls[i]
		byName["--"+f.name] = f
		if f.short != "" {
			byName["-"+f.short] = f
		}
		if f.def != "" {
			o.S[f.name] = f.def
		}
	}
	bad := func(format string, a ...any) error {
		return die("usage: moon issues %s [options]\nmoon issues %s: error: %s", name, name, fmt.Sprintf(format, a...))
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			o.Pos = append(o.Pos, arg)
			continue
		}
		key, val, hasVal := strings.Cut(arg, "=")
		f := byName[key]
		if f == nil {
			return nil, bad("unrecognized arguments: %s", arg)
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
		choices := f.choices
		if f.name == "repo" {
			choices = aliases
		}
		if len(choices) > 0 && !contains(choices, val) {
			return nil, bad("argument --%s: invalid choice: '%s' (choose from %s)", f.name, val, strings.Join(choices, ", "))
		}
		o.Seen[f.name] = true
		if f.kind == 'a' {
			o.L[f.name] = append(o.L[f.name], val)
		} else {
			o.S[f.name] = val
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
	Now    func() time.Time
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
	if len(rest) == 0 || rest[0] == "-h" || rest[0] == "--help" || rest[0] == "help" {
		fmt.Fprint(e.Out, usageText)
		if len(rest) == 0 {
			return 2
		}
		return 0
	}
	name := rest[0]
	rest = rest[1:]
	if name == "pr" {
		if len(rest) == 0 || rest[0] != "show" {
			fmt.Fprint(e.Err, "usage: moon issues pr show <ref>\n")
			return 2
		}
		name, rest = "pr show", rest[1:]
	}
	sp := commands()[name]
	if sp == nil {
		fmt.Fprintf(e.Err, "moon issues: unknown command %q\n%s", name, usageText)
		return 2
	}
	p, err := FindConfig(cfgPath, e.Getenv("MOON_CONFIG"), e.Cwd)
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
	a := &App{Home: home, GH: gh, Out: e.Out, Err: e.Err, Stdin: e.Stdin, Now: e.Now, Host: host, Environ: e.Getenv,
		Repos: cfg.Repos, SpawnSync: spawnSync}
	o, err := parseOpts(sp, name, rest, a.Shorts())
	if err != nil {
		fmt.Fprintln(e.Err, err)
		return 2
	}
	if sp.read {
		if err := a.autosync(name, o, cfgPath); err != nil {
			fmt.Fprintln(e.Err, err)
			return 2
		}
	}
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
	if o.B["fresh"] { // M_fresh
		a.doSync(shorts, false, a.Err, "")
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
	cmd := exec.Command(self, "issues", "--dir", a.Home, "sync", "--quiet")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "ISSUES_NO_AUTOSYNC=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
