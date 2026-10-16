package issues

import (
	"fmt"
	"sort"
	"strings"
)

type helpEntry struct {
	summary  string
	usage    string
	examples [2]string
}

var helpTexts = map[string]helpEntry{
	"ls": {"list issues from the local mirror, with their work status", "moon issues ls [--repo R] [--state open|closed|all] [--label L] [--milestone M] [-q text] [-w] [-i] [--status S[,S]] [-B full|short|none]",
		[2]string{"moon issues ls -L bug -s up -l 10", "moon issues ls -w   # what agents are working on; -i = idle, -s status = by status"}},
	"show": {"print one issue with its comments", "moon issues show <ref>",
		[2]string{"moon issues show app#12", "moon issues show app#L-20260301120000-ab12   # an issue not pushed yet"}},
	"milestones": {"list milestones with progress (open ones on a terminal, all when piped)", "moon issues milestones [--repo R] [--state open|closed|all] [--sort due|progress|title|open] [-r] [--limit N]",
		[2]string{"moon issues milestones -s due", "moon issues milestones -S all -j"}},
	"milestones show": {"one milestone's progress and its issues", "moon issues milestones show <title> [--repo R] [--state open|closed|all] [--label L] [--sort ...] [--limit N]",
		[2]string{"moon issues milestones show v1.0", "moon issues milestones show v1 -S all -s t   # a unique prefix is enough"}},
	"milestones new": {"create a milestone (queued locally as M-<n>; sync creates it on GitHub before the issues that use it)",
		"moon issues milestones new <title> [-d YYYY-MM-DD] [-D text | --description-file F|-] [-R repo]",
		[2]string{"moon issues milestones new \"v1.0\" -d 2026-12-31 -D \"first public release\"", "moon issues milestones new \"auth rewrite\" --description-file plan.md -R app   # then: moon issues new -R app -m M-1 ..."}},
	"milestones edit": {"change a milestone's title, due date or description (queued, sent by sync)",
		"moon issues milestones edit <title|M-n> [--title T] [-d YYYY-MM-DD|none] [-D text | --description-file F|-] [-R repo]",
		[2]string{"moon issues milestones edit v1.0 -d 2027-01-31", "moon issues milestones edit M-2 --title \"auth rewrite v2\" -d none -D \"scope cut\""}},
	"milestones close": {"close a milestone (queued, sent by sync)", "moon issues milestones close <title|M-n> [-R repo]",
		[2]string{"moon issues milestones close v1.0", "moon issues milestones close M-2 -R app"}},
	"milestones reopen": {"reopen a closed milestone (queued, sent by sync)", "moon issues milestones reopen <title|M-n> [-R repo]",
		[2]string{"moon issues milestones reopen v1.0", "moon issues milestones reopen M-2 -R app"}},
	"prs": {"list pull requests from the mirror", "moon issues prs [--repo R] [--state open|merged|closed|all] [--since ISO] [--json]",
		[2]string{"moon issues prs -S merged --since 2026-01-01T00:00:00Z", "moon issues prs -S all -s up -l 5"}},
	"pr show": {"print one pull request", "moon issues pr show <ref>",
		[2]string{"moon issues pr show app#31", "moon issues pr show lib#4"}},
	"frontier": {"open issues nobody has claimed", "moon issues frontier [--repo R] [--label L] [--milestone M] [-i] [--status S[,S]] [-B full|short|none]",
		[2]string{"moon issues frontier -L kind:bug", "moon issues frontier -s t -l 5   # or just: moon issues frontier 5"}},
	"claim": {"claim an issue for an agent (exit 3 when someone else holds it)", "moon issues claim <ref> --agent NAME [--ttl 120m] [--pid N]",
		[2]string{"moon issues claim app#12 -a alice", "moon issues claim app#12 -a alice --ttl 4h"}},
	"release": {"drop a claim", "moon issues release <ref> (--agent NAME | --force)",
		[2]string{"moon issues release app#12 -a alice", "moon issues release app#12 --force"}},
	"claims": {"list claims with the work status of their issue (a claim on a closed issue is flagged)", "moon issues claims [--show-status]",
		[2]string{"moon issues claims", "moon issues claims -j"}},
	"sweep": {"list claims that are stale or on closed issues; --release removes them (dry run by default)", "moon issues sweep [--release]",
		[2]string{"moon issues sweep", "moon issues sweep --release"}},
	"status": {"outbox and last-pull summary", "moon issues status",
		[2]string{"moon issues status", "moon issues status -t"}},
	"new": {"queue a new issue (sent by sync)", "moon issues new --repo R --title T (--body B | --body-file F|-) [--label L ...] [--milestone M] [--now]",
		[2]string{"moon issues new --repo app --title \"login: button is dead\" --body \"steps...\" --label bug", "echo details | moon issues new --repo app --title \"docs: typo\" --body-file - --now"}},
	"comment": {"queue a comment", "moon issues comment <ref> (--body B | --body-file F|-)",
		[2]string{"moon issues comment app#12 --body \"on it\"", "moon issues comment app#12 --body-file notes.md"}},
	"close": {"queue closing an issue", "moon issues close <ref> [--reason completed|not_planned] [--comment C] [--agent NAME]",
		[2]string{"moon issues close app#12", "moon issues close app#12 -a alice --reason not_planned --comment \"duplicate of #9\""}},
	"reopen": {"queue reopening an issue", "moon issues reopen <ref>",
		[2]string{"moon issues reopen app#12", "moon issues reopen lib#3"}},
	"label": {"queue label changes", "moon issues label <ref> [--add L ...] [--remove L ...]",
		[2]string{"moon issues label app#12 --add bug --add urgent", "moon issues label app#12 --remove urgent"}},
	"milestone": {"queue setting a milestone (use none to clear)", "moon issues milestone <ref> <title|none>",
		[2]string{"moon issues milestone app#12 v1.0", "moon issues milestone app#12 none"}},
	"edit": {"queue a title or body edit", "moon issues edit <ref> [--title T] [--body B | --body-file F|-]",
		[2]string{"moon issues edit app#12 --title \"login: button is dead on Safari\"", "moon issues edit app#12 --body-file new-body.md"}},
	"push": {"send queued writes to GitHub", "moon issues push [--repo R]",
		[2]string{"moon issues push", "moon issues push -R app"}},
	"pull": {"refresh the mirror from GitHub", "moon issues pull [--repo R] [--full]",
		[2]string{"moon issues pull", "moon issues pull -R app --full"}},
	"sync": {"push, then pull", "moon issues sync [--repo R] [--quiet]",
		[2]string{"moon issues sync", "moon issues sync -R app --quiet"}},
	"prune": {"delete mirror files of old closed items", "moon issues prune [--repo R]",
		[2]string{"moon issues prune", "ISSUES_KEEP_DAYS=7 moon issues prune --repo app"}},
}

var flagHelp = map[string]string{
	"repo": "only this repo alias", "fresh": "sync first, then read", "state": "which items", "label": "only this label",
	"milestone": "only this milestone (case-insensitive, or a unique prefix)", "q": "substring of title or labels", "format": "auto (table on a terminal, tsv when piped), table, tsv, json",
	"no-color": "no colours (also off when NO_COLOR is set or stdout is not a terminal)",
	"sort":     "number, created, updated: newest first; title: A-Z; due: soonest first; progress, open: highest first", "reverse": "reverse the sort order", "limit": "show at most N rows (0 = all; default 30 on a terminal)",
	"json": "same as --format json", "since": "only items updated since this ISO time", "agent": "who you are", "ttl": "how long the claim lasts (e.g. 90m, 4h)",
	"pid": "process that holds the claim", "force": "ignore who holds it", "title": "title", "body": "body text", "body-file": "read the body from a file, or - for stdin",
	"add": "label to add (repeatable)", "remove": "label to remove (repeatable)", "now": "send it right away", "reason": "close reason",
	"comment": "comment to post with the close",
	"working": "only issues being worked on now (status working)", "idle": "only issues nobody is on (status ready or open)",
	"status":      "only these statuses, comma-separated: stale, working, in-review, blocked, ready, open, done (-w, -i and --status add up)",
	"show-status": "add the work status as a trailing column (or line) to piped output", "release": "remove them (without it, only list)",
	"labels": "full (stored names), short (kind:/area:/triage: dropped) or none; default by terminal width",
	"color":  "auto, always (even piped, for less -R; needs a table), never", "full": "re-read everything", "quiet": "no output",
	"due": "due date, YYYY-MM-DD (edit: none clears it)", "description": "description text", "description-file": "read the description from a file, or - for stdin",
}

// commandNames lists what `moon issues <name>` accepts, with "pr show" as "pr".
func commandNames() []string {
	var out []string
	for n := range commands() {
		if n == "pr show" {
			n = "pr"
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func cmdHelp(name string) string {
	h := helpTexts[name]
	sp := commands()[name]
	var b strings.Builder
	fmt.Fprintf(&b, "moon issues %s: %s\n\nusage: %s\n", name, h.summary, h.usage)
	if sp != nil && len(sp.fls) > 0 {
		b.WriteString("\noptions:\n")
		for _, f := range sp.fls {
			left := "--" + f.name
			if k := f.shortKey(); k != "" {
				left = "-" + k + ", " + left
			}
			if f.kind != 'b' {
				switch {
				case len(f.choices) > 0:
					left += " {" + strings.Join(f.choices, "|") + "}"
				default:
					left += " VALUE"
				}
			}
			desc := flagHelp[f.name]
			if f.name == "format" {
				desc += "; -j = json, -t = tsv"
			}
			if f.def != "" {
				desc += " (default " + f.def + ")"
			}
			fmt.Fprintf(&b, "  %-26s %s\n", left, strings.TrimSpace(desc))
		}
	}
	b.WriteString("\nexamples:\n")
	for _, ex := range h.examples {
		b.WriteString("  " + ex + "\n")
	}
	return b.String()
}

// helpFlagSeen reports whether args ask for help, ignoring a -h/--help that is the value of another flag.
func helpFlagSeen(sp *spec, args []string) bool {
	val := map[string]bool{}
	for _, f := range sp.fls {
		if f.kind != 'b' {
			val["--"+f.name] = true
			if k := f.shortKey(); k != "" {
				val["-"+k] = true
			}
		}
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			return true
		case val[a]:
			i++
		}
	}
	return false
}

func unknownCommand(name string) string {
	msg := fmt.Sprintf("moon issues: unknown command %q", name)
	if c := closest(name, append(commandNames(), "help")); c != "" {
		msg += fmt.Sprintf(" (did you mean %s?)", c)
	}
	return msg + "\nrun `moon issues help` for the list of commands\n"
}
