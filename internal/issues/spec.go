package issues

import (
	"sort"
	"strings"
)

// This file is the read-only view of the command table (commands, helpTexts, flagHelp, shortMap) that
// shell completion and the TUI command line use. There is no second copy of any flag: help, the parser
// and completion all read the same table, and spec_test.go fails when a usage line and the table disagree.

// FlagInfo describes one option of a command.
type FlagInfo struct {
	Name    string   // long name without dashes; empty for a bare short shortcut such as -j
	Short   string   // single letter or ""
	Aliases []string // other long names (--query for -q)
	Takes   bool     // takes a value
	Repeat  bool     // may be given more than once
	Kind    string   // what the value is: enum, repo, label, milestone, file, number, text
	Choices []string // for enum
	Default string
	Desc    string
}

// CmdInfo describes one command, for example "ls" or "pr show".
type CmdInfo struct {
	Name    string // "ls", "pr show", "milestones show"
	Summary string
	Usage   string
	Flags   []FlagInfo
	Pos     []string // kinds of the positional arguments: ref, milestone
	Read    bool
}

// posKinds is what each positional argument of a command is. Its length must equal the parser's npos.
var posKinds = map[string][]string{
	"show": {"ref"}, "pr show": {"ref"}, "claim": {"ref"}, "release": {"ref"}, "comment": {"ref"}, "close": {"ref"},
	"reopen": {"ref"}, "label": {"ref"}, "edit": {"ref"}, "milestone": {"ref", "milestone"}, "milestones show": {"milestone"},
	"milestones new": {"text"}, "milestones edit": {"milestone"}, "milestones close": {"milestone"}, "milestones reopen": {"milestone"},
}

var valueKinds = map[string]string{"repo": "repo", "label": "label", "add": "label", "remove": "label", "milestone": "milestone",
	"body-file": "file", "limit": "number", "status": "status", "due": "date", "description-file": "file"}

// GlobalFlags are the options `moon issues` takes before the command.
func GlobalFlags() []FlagInfo {
	return []FlagInfo{
		{Name: "config", Takes: true, Kind: "file", Desc: "path to moon.toml"},
		{Name: "dir", Takes: true, Kind: "file", Desc: "mirror directory"},
	}
}

// Describe lists every `moon issues` command with its flags, sorted by name.
func Describe() []CmdInfo {
	cmds := commands()
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []CmdInfo
	for _, n := range names {
		sp := cmds[n]
		h := helpTexts[n]
		ci := CmdInfo{Name: n, Summary: h.summary, Usage: h.usage, Pos: posKinds[n], Read: sp.read}
		hasFormat := false
		for _, f := range sp.fls {
			fi := FlagInfo{Name: f.name, Short: f.shortKey(), Takes: f.kind != 'b', Repeat: f.kind == 'a', Choices: f.choices, Default: f.def,
				Desc: flagHelp[f.name]}
			if f.name == "q" {
				fi.Aliases = []string{"query"}
			}
			if fi.Takes {
				fi.Kind = valueKinds[f.name]
				if fi.Kind == "" {
					fi.Kind = "text"
					if len(f.choices) > 0 {
						fi.Kind = "enum"
					}
				}
			}
			if f.name == "repo" {
				fi.Kind = "repo"
			}
			if f.name == "format" {
				hasFormat = true
				fi.Desc = strings.SplitN(fi.Desc, " (", 2)[0]
			}
			ci.Flags = append(ci.Flags, fi)
		}
		if hasFormat {
			ci.Flags = append(ci.Flags, FlagInfo{Short: "j", Desc: "same as --format json"}, FlagInfo{Short: "t", Desc: "same as --format tsv"})
		}
		ci.Flags = append(ci.Flags, FlagInfo{Name: "help", Short: "h", Desc: "show the options and two examples"})
		out = append(out, ci)
	}
	return out
}

// MatchLabel resolves what a person typed for -L to stored labels, exactly as `ls -L` does: the stored name,
// else the shown short name (bug for kind:bug) or any suffix after a colon.
func MatchLabel(want string, universe []string) []string { return matchLabel(want, universe) }

// NormStatus maps what a person types for --status (in-review, review, WORKING) to a status name, "" if unknown.
func NormStatus(s string) string { return normStatus(s) }
