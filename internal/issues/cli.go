package issues

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	pos  []string // kinds of the positional arguments (ref, milestone, text, op); its length is how many the parser wants
	help helpEntry
	list bool // prints a list: on a terminal it starts with the scope header
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
		"ls": {help: helpEntry{"list issues from the local mirror, with their work status", "moon issues ls [--repo R] [--state open|closed|all] [--label L] [--milestone M] [-q text] [-w] [-i] [--status S[,S]] [-B full|short|none]", [2]string{"moon issues ls -L bug -s up -l 10", "moon issues ls -w   # what agents are working on; -i = idle, -s status = by status"}}, list: true, fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "closed", "all"}, def: "open"},
			fl{name: "label", kind: 's'}, fl{name: "milestone", kind: 's'}, fl{name: "q", short: "q", kind: 's'}).with(fmtFls(), issueListFls()), read: true, run: (*App).cmdLs},
		"show": {pos: []string{"ref"}, help: helpEntry{"print one issue with its comments", "moon issues show <ref>", [2]string{"moon issues show app#12", "moon issues show app#L-20260301120000-ab12   # an issue not pushed yet"}}, fls: one(freshFl(), fl{name: "show-status", kind: 'b'}).with(fmtFls()), read: true, run: (*App).cmdShow},
		"milestones": {help: helpEntry{"list milestones with progress (open ones on a terminal, all when piped)", "moon issues milestones [--repo R] [--state open|closed|all] [--sort due|progress|title|open] [-r] [--limit N]", [2]string{"moon issues milestones -s due", "moon issues milestones -S all -j"}}, list: true, fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "closed", "all"}}).with(fmtFls(),
			[]fl{{name: "sort", kind: 's', choices: []string{"due", "progress", "title", "open"}}, {name: "reverse", short: "r", kind: 'b'}, {name: "limit", kind: 's'}}),
			read: true, run: (*App).cmdMilestones},
		"milestones show": {pos: []string{"milestone"}, help: helpEntry{"one milestone's progress and its issues", "moon issues milestones show <title> [--repo R] [--state open|closed|all] [--label L] [--sort ...] [--limit N]", [2]string{"moon issues milestones show v1.0", "moon issues milestones show v1 -S all -s t   # a unique prefix is enough"}}, list: true, fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "closed", "all"}, def: "open"},
			fl{name: "label", kind: 's'}).with(fmtFls(), issueListFls()), read: true, run: (*App).cmdMilestoneShow},
		"milestones new": {pos: []string{"text"}, help: helpEntry{"create a milestone (queued locally as M-<n>; sync creates it on GitHub before the issues that use it)", "moon issues milestones new <title> [-d YYYY-MM-DD] [-D text | --description-file F|-] [-R repo]", [2]string{"moon issues milestones new \"v1.0\" -d 2026-12-31 -D \"first public release\"", "moon issues milestones new \"auth rewrite\" --description-file plan.md -R app   # then: moon issues new -R app -m M-1 ..."}}, fls: one(repoFl(), fl{name: "due", kind: 's'}, fl{name: "description", kind: 's'}, fl{name: "description-file", kind: 's'}),
			run: (*App).cmdMsNew},
		"milestones edit": {pos: []string{"milestone"}, help: helpEntry{"change a milestone's title, due date or description (queued, sent by sync)", "moon issues milestones edit <title|M-n> [--title T] [-d YYYY-MM-DD|none] [-D text | --description-file F|-] [-R repo]", [2]string{"moon issues milestones edit v1.0 -d 2027-01-31", "moon issues milestones edit M-2 --title \"auth rewrite v2\" -d none -D \"scope cut\""}}, fls: one(repoFl(), fl{name: "title", kind: 's'}, fl{name: "due", kind: 's'}, fl{name: "description", kind: 's'},
			fl{name: "description-file", kind: 's'}), run: (*App).cmdMsEdit},
		"labels new":        {pos: []string{"text"}, help: helpEntry{"create a label (queued locally; sync creates it on GitHub before the issues that use it)", "moon issues labels new <name> [--color RRGGBB] [-D text] [-R repo]", [2]string{"moon issues labels new area::sim --color 1d76db", "moon issues labels new area::sim -R falcon && moon issues new -R falcon -L area::sim ..."}}, fls: one(repoFl(), fl{name: "color", kind: 's'}, fl{name: "description", kind: 's'}), run: (*App).cmdLabelNew},
		"milestones close":  {pos: []string{"milestone"}, help: helpEntry{"close a milestone (queued, sent by sync)", "moon issues milestones close <title|M-n> [-R repo]", [2]string{"moon issues milestones close v1.0", "moon issues milestones close M-2 -R app"}}, fls: one(repoFl()), run: func(a *App, o *Opts) error { return a.cmdMsState("ms_close")(o) }},
		"milestones reopen": {pos: []string{"milestone"}, help: helpEntry{"reopen a closed milestone (queued, sent by sync)", "moon issues milestones reopen <title|M-n> [-R repo]", [2]string{"moon issues milestones reopen v1.0", "moon issues milestones reopen M-2 -R app"}}, fls: one(repoFl()), run: func(a *App, o *Opts) error { return a.cmdMsState("ms_reopen")(o) }},
		"prs": {help: helpEntry{"list pull requests from the mirror", "moon issues prs [--repo R] [--state open|merged|closed|all] [--since ISO] [--json]", [2]string{"moon issues prs -S merged --since 2026-01-01T00:00:00Z", "moon issues prs -S all -s up -l 5"}}, list: true, fls: one(repoFl(), freshFl(), fl{name: "state", kind: 's', choices: []string{"open", "merged", "closed", "all"}, def: "open"},
			fl{name: "since", kind: 's'}, fl{name: "json", kind: 'b'}).with(fmtFls(), sortFls()), read: true, run: (*App).cmdPRs},
		"pr show":  {pos: []string{"ref"}, help: helpEntry{"print one pull request", "moon issues pr show <ref>", [2]string{"moon issues pr show app#31", "moon issues pr show lib#4"}}, fls: one(freshFl()), read: true, run: (*App).cmdPRShow},
		"frontier": {help: helpEntry{"open issues nobody has claimed", "moon issues frontier [--repo R] [--label L] [--milestone M] [-i] [--status S[,S]] [-B full|short|none]", [2]string{"moon issues frontier -L kind:bug", "moon issues frontier -s t -l 5   # or just: moon issues frontier 5"}}, list: true, fls: one(repoFl(), freshFl(), fl{name: "label", kind: 's'}, fl{name: "milestone", kind: 's'}).with(fmtFls(), issueListFls()), read: true, run: (*App).cmdFrontier},
		"claim": {pos: []string{"ref"}, help: helpEntry{"claim an issue for an agent (exit 3 when someone else holds it)", "moon issues claim <ref> --agent NAME [--ttl 120m] [--pid N]   (--ttl default: claim_ttl in moon.toml, else 120m; claim again with the same --agent to renew the lease)", [2]string{"moon issues claim app#12 -a alice", "moon issues claim app#12 -a alice --ttl 30m"}}, fls: one(fl{name: "agent", kind: 's', req: true}, fl{name: "ttl", kind: 's'}, fl{name: "pid", kind: 's', def: "0"}),
			run: (*App).cmdClaim},
		"release": {pos: []string{"ref"}, help: helpEntry{"drop a claim", "moon issues release <ref> (--agent NAME | --force)", [2]string{"moon issues release app#12 -a alice", "moon issues release app#12 --force"}}, fls: one(fl{name: "agent", kind: 's'}, fl{name: "force", kind: 'b'}), run: (*App).cmdRelease},
		"claims":  {help: helpEntry{"list claims with the work status of their issue (a claim on a closed issue is flagged)", "moon issues claims [--show-status]", [2]string{"moon issues claims", "moon issues claims -j"}}, list: true, fls: one(freshFl(), fl{name: "show-status", kind: 'b'}).with(fmtFls()), read: true, run: (*App).cmdClaims},
		"sweep":   {help: helpEntry{"list claims that are stale or on closed issues; --release removes them (dry run by default)", "moon issues sweep [--release] [--live] [--agent NAME]", [2]string{"moon issues sweep --release", "moon issues sweep --live --agent alice --release   # stop work: drop all of alice's claims"}}, list: true, fls: one(freshFl(), fl{name: "release", kind: 'b'}, fl{name: "live", kind: 'b'}, fl{name: "agent", kind: 's'}).with(fmtFls()), read: true, run: (*App).cmdSweep},
		"status":  {help: helpEntry{"outbox, autosync (on or off, last run) and last-pull summary", "moon issues status", [2]string{"moon issues status", "moon issues status -t"}}, list: true, fls: fmtFls(), run: (*App).cmdStatus},
		"new": {help: helpEntry{"queue a new issue (sent by sync)", "moon issues new --repo R --title T (--body B | --body-file F|-) [--label L ...] [--milestone M] [--depends-on REF ...] [--now] [--allow-empty-body]", [2]string{"moon issues new --repo app --title \"login: button is dead\" --body \"steps...\" --label bug", "echo details | moon issues new --repo app --title \"docs: typo\" --body-file - --now"}}, fls: cat(one(repoFl(), fl{name: "title", kind: 's', req: true}, fl{name: "label", kind: 'a'},
			fl{name: "milestone", kind: 's'}, fl{name: "now", kind: 'b'}, fl{name: "depends-on", kind: 'a'}, emptyBodyFl()), bodyFls()), run: (*App).cmdNew},
		"comment": {pos: []string{"ref"}, help: helpEntry{"queue a comment", "moon issues comment <ref> (--body B | --body-file F|-)", [2]string{"moon issues comment app#12 --body \"on it\"", "moon issues comment app#12 --body-file notes.md"}}, fls: bodyFls(), run: func(a *App, o *Opts) error { return a.cmdSimple("comment")(o) }},
		"close": {pos: []string{"ref"}, help: helpEntry{"queue closing an issue", "moon issues close <ref> [--reason completed|not_planned] [--comment C] [--agent NAME]", [2]string{"moon issues close app#12", "moon issues close app#12 -a alice --reason not_planned --comment \"duplicate of #9\""}}, fls: one(fl{name: "reason", kind: 's', choices: []string{"completed", "not_planned"}, def: "completed"},
			fl{name: "comment", kind: 's'}, fl{name: "agent", kind: 's'}), run: func(a *App, o *Opts) error { return a.cmdSimple("close")(o) }},
		"reopen": {pos: []string{"ref"}, help: helpEntry{"queue reopening an issue", "moon issues reopen <ref>", [2]string{"moon issues reopen app#12", "moon issues reopen lib#3"}}, run: func(a *App, o *Opts) error { return a.cmdSimple("reopen")(o) }},
		"label": {pos: []string{"ref"}, help: helpEntry{"queue label changes", "moon issues label <ref> [--add L ...] [--remove L ...]", [2]string{"moon issues label app#12 --add bug --add urgent", "moon issues label app#12 --remove urgent"}}, fls: one(fl{name: "add", kind: 'a'}, fl{name: "remove", kind: 'a'}),
			run: func(a *App, o *Opts) error { return a.cmdSimple("label")(o) }},
		"milestone": {pos: []string{"ref", "milestone"}, help: helpEntry{"queue setting a milestone (use none to clear)", "moon issues milestone <ref> <title|none>", [2]string{"moon issues milestone app#12 v1.0", "moon issues milestone app#12 none"}}, run: func(a *App, o *Opts) error { return a.cmdSimple("milestone")(o) }},
		"edit": {pos: []string{"ref"}, help: helpEntry{"queue a title or body edit", "moon issues edit <ref> [--title T] [--body B | --body-file F|-] [--depends-on REF ...|none] [--allow-empty-body]", [2]string{"moon issues edit app#12 --title \"login: button is dead on Safari\"", "moon issues edit app#12 --depends-on 9 --depends-on 10   # body gets: Depends on #9, #10"}}, fls: cat(one(fl{name: "title", kind: 's'}, fl{name: "depends-on", kind: 'a'}, emptyBodyFl()), bodyFls()),
			run: func(a *App, o *Opts) error { return a.cmdSimple("edit")(o) }},
		"push":         {help: helpEntry{"send queued writes to GitHub", "moon issues push [--repo R]", [2]string{"moon issues push", "moon issues push -R app"}}, fls: one(repoFl()), run: (*App).cmdPush},
		"pull":         {help: helpEntry{"refresh the mirror from GitHub", "moon issues pull [--repo R] [--full]", [2]string{"moon issues pull", "moon issues pull -R app --full"}}, fls: one(repoFl(), fl{name: "full", kind: 'b'}), run: (*App).cmdPull},
		"prune":        {help: helpEntry{"delete mirror files of old closed items", "moon issues prune [--repo R]", [2]string{"moon issues prune", "ISSUES_KEEP_DAYS=7 moon issues prune --repo app"}}, fls: one(repoFl()), run: (*App).cmdPrune},
		"sync":         {help: helpEntry{"push, then pull", "moon issues sync [--repo R] [--quiet]", [2]string{"moon issues sync", "moon issues sync -R app --quiet"}}, fls: one(repoFl(), fl{name: "quiet", kind: 'b'}), run: (*App).cmdSync},
		"where":        {help: helpEntry{"show which repos commands default to here, why, and which rule matched", "moon issues where [-R alias ...] [--all]", [2]string{"moon issues where", "moon issues where -R all   # what --all would look at"}}, run: (*App).cmdWhere},
		"outbox ls":    {help: helpEntry{"list the queued writes with their last error (what sync will send)", "moon issues outbox [ls] [--repo R] [--format auto|table|tsv|json]", [2]string{"moon issues outbox", "moon issues outbox ls -j   # args included"}}, fls: fmtFls(), run: (*App).cmdOutboxLs},
		"outbox retry": {pos: []string{"op"}, help: helpEntry{"send a write again that sync gave up on (GitHub said a retry cannot work)", "moon issues outbox retry <op>", [2]string{"moon issues outbox retry 3f9a1c2b", "moon issues outbox ls   # shows which writes sync gave up on, and why"}}, run: (*App).cmdOutboxRetry},
		"outbox drop":  {pos: []string{"op"}, help: helpEntry{"remove a queued write; a queued new issue with writes queued on it needs --cascade", "moon issues outbox drop <op> [--cascade]", [2]string{"moon issues outbox drop 3f9a21c0", "moon issues outbox drop 3f9a21c0 --cascade   # also drop the writes queued on that new issue"}}, fls: one(fl{name: "cascade", kind: 'b'}), run: (*App).cmdOutboxDrop},
		"outbox edit": {pos: []string{"op"}, help: helpEntry{"change a queued write that sync cannot send (a bad label on a new issue, a wrong title or body)", "moon issues outbox edit <op> [--title T] [--body B | --body-file F|-] [--label L ...] [--milestone M] [--add L ...] [--remove L ...] [--allow-empty-body]", [2]string{"moon issues outbox edit 3f9a21c0 --label bug --label area::web   # new: replaces its labels", "moon issues outbox edit 3f9a21c0 --body-file fixed.md   # <op> is a piece of the op id from outbox ls"}}, fls: cat(one(fl{name: "title", kind: 's'}, fl{name: "label", kind: 'a'}, fl{name: "milestone", kind: 's'},
			fl{name: "add", kind: 'a'}, fl{name: "remove", kind: 'a'}, emptyBodyFl()), bodyFls()), run: (*App).cmdOutboxEdit},
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
Autosync: a read of an old mirror starts a background sync (it also sends the queued writes; log: <mirror>/autosync.log).
  Turn it off: MOON_AUTOSYNC=0 (or ISSUES_NO_AUTOSYNC=1) in the environment, or autosync = false in moon.toml; MOON_AUTOSYNC=1
  turns it on again over the file. "moon issues status" shows whether it is on and how its last run went. "moon issues sync" runs it by hand.
Aliases come from [repos.<alias>] in moon.toml. Env: MOON_AUTOSYNC, ISSUES_NO_AUTOSYNC, ISSUES_KEEP_DAYS, ISSUES_GH, NO_COLOR.
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
	if lf := byName["--limit"]; lf != nil && len(sp.pos) == 0 && len(o.Pos) == 1 && !o.Seen["limit"] {
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
	if len(o.Pos) != len(sp.pos) {
		return nil, bad("expected %d positional argument(s), got %d", len(sp.pos), len(o.Pos))
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
		if commands()[name] == nil {
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
	if sp.takesRef() {
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
		if err := a.BeforeRead(name, o); err != nil {
			fmt.Fprintln(e.Err, err)
			return 2
		}
	}
	a.printScopeHeader(sp, o)
	if !sp.read && !noAutopush[name] {
		defer a.AfterWrite() // after the write is queued, even when the command then fails
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
