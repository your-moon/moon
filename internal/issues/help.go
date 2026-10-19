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

var flagHelp = map[string]string{
	"repo": "only this repo alias (repeatable); -R all = every repo. Default: the project you are in, else all repos",
	"all":  "every configured repo, not only the project you are in (same as -R all)", "fresh": "sync first, then read", "state": "which items", "label": "only this label",
	"milestone": "only this milestone (case-insensitive, or a unique prefix)", "q": "substring of title or labels", "format": "auto (table on a terminal, tsv when piped), table, tsv, json",
	"no-color": "no colours (also off when NO_COLOR is set or stdout is not a terminal)",
	"sort":     "number, created, updated: newest first; title: A-Z; due: soonest first; progress, open: highest first", "reverse": "reverse the sort order", "limit": "show at most N rows (0 = all; default 30 on a terminal)",
	"json": "same as --format json", "since": "only items updated since this ISO time", "agent": "who you are", "ttl": "how long the claim lasts (e.g. 90m, 4h)",
	"pid": "process that holds the claim", "force": "ignore who holds it", "title": "title", "body": "body text", "body-file": "read the body from a file, or - for stdin",
	"add": "label to add (repeatable)", "remove": "label to remove (repeatable)", "now": "send it right away", "reason": "close reason",
	"comment":          "comment to post with the close",
	"allow-empty-body": "accept a blank body (default: refused, because an empty --body-file usually means a script failed and edit would erase the issue body)",
	"working":          "only issues being worked on now (status working)", "idle": "only issues nobody is on (status ready or open)",
	"status":      "only these statuses, comma-separated: stale, working, in-review, blocked, ready, open, done (-w, -i and --status add up)",
	"show-status": "add the work status as a trailing column (or line) to piped output", "release": "remove them (without it, only list)",
	"live":   "also list live claims on open issues, for when an agent was stopped and cannot release (with --release they are dropped)",
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
	sp := commands()[name]
	h := sp.help
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
