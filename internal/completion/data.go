package completion

import (
	"sort"
	"strconv"
	"strings"

	"github.com/your-moon/moon/internal/issues"
)

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func inAliases(alias string, only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, a := range only {
		if a == alias {
			return true
		}
	}
	return false
}

// ShortLabel is how a label is displayed in short mode: "kind:bug" shows as "bug", "area::auth" as "auth".
// `ls -L` accepts it as well as the stored name.
func ShortLabel(l string) string { return issues.LabelShort(l) }

// RefFilter says which issues a command offers to agent: for an item action, the ones its Allows says yes to
// (internal/issues/actions.go); everything else offers all of them.
func RefFilter(cmd, agent string) func(issues.Item) bool {
	if a, ok := issues.ActionNamed(cmd); ok {
		return func(it issues.Item) bool { return a.Allows(it, agent) }
	}
	return func(issues.Item) bool { return true }
}

// Refs lists alias#NNN candidates for cmd that start with typed, newest first, at most MaxRefs, with the
// title as the description. Only the mirror is read.
func Refs(s Source, cmd, typed string, aliases []string) []Candidate {
	type row struct {
		ref, title, updated string
		num                 int
	}
	var rows []row
	if cmd == "pr show" {
		for _, p := range s.PRs() {
			if inAliases(p.Alias, aliases) && strings.HasPrefix(p.Ref, typed) {
				rows = append(rows, row{p.Ref, p.Title, p.Updated, p.Num})
			}
		}
	} else {
		keep := RefFilter(cmd, agentOf(s))
		for _, it := range s.Items() {
			if inAliases(it.Alias, aliases) && strings.HasPrefix(it.Ref, typed) && keep(it) {
				rows = append(rows, row{it.Ref, it.Title, it.Updated, it.Num})
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case a.updated != b.updated:
			return a.updated > b.updated
		case a.num != b.num:
			return a.num > b.num
		}
		return a.ref < b.ref
	})
	if len(rows) > MaxRefs {
		rows = rows[:MaxRefs]
	}
	out := make([]Candidate, len(rows))
	for i, r := range rows {
		out[i] = Candidate{r.ref, oneLine(r.title)}
	}
	return out
}

// Labels lists every label of the mirror under its stored name and, when different, its short display name.
func Labels(s Source, aliases []string) []Candidate {
	count := map[string]int{}
	for _, it := range s.Items() {
		if !inAliases(it.Alias, aliases) {
			continue
		}
		for _, l := range it.Labels {
			count[l]++
		}
	}
	var stored []string
	for l := range count {
		stored = append(stored, l)
	}
	sort.Strings(stored)
	have := map[string]bool{}
	for _, l := range stored {
		have[l] = true
	}
	var out []Candidate
	for _, l := range stored {
		out = append(out, Candidate{l, plural(count[l], "issue")})
		if sh := ShortLabel(l); sh != l && sh != "" && !have[sh] {
			have[sh] = true
			out = append(out, Candidate{sh, "short for " + l})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

func plural(n int, w string) string {
	if n != 1 {
		w += "s"
	}
	return strconv.Itoa(n) + " " + w
}

// Milestones lists the milestone titles of the mirror (milestones.tsv, queued ones, titles only seen on issues)
// and the local ids (M-1) of the milestones created here.
func Milestones(s Source, aliases []string) []Candidate { return MilestonesFor(s, aliases, "", true) }

// MilestoneTitles is Milestones without the M-<n> ids: what a picker shows.
func MilestoneTitles(s Source, aliases []string) []Candidate {
	return MilestonesFor(s, aliases, "", false)
}

// MilestonesFor is the list for one command: state "open" or "closed" keeps only milestones in that state
// (close offers open ones, reopen closed ones); "" keeps all. ids adds the local M-<n> ids.
func MilestonesFor(s Source, aliases []string, state string, ids bool) []Candidate {
	seen := map[string]bool{}
	var out []Candidate
	for _, r := range s.Milestones() {
		if !inAliases(r.Alias, aliases) || r.Title == "" || (state != "" && r.State != state) {
			continue
		}
		if ids && r.LocalID != "" && !seen[r.LocalID] {
			seen[r.LocalID] = true
			out = append(out, Candidate{r.LocalID, "local id of " + r.Title})
		}
		if seen[r.Title] {
			continue
		}
		seen[r.Title] = true
		out = append(out, Candidate{r.Title, r.State + ", " + strconv.Itoa(r.Closed) + "/" + strconv.Itoa(r.Closed+r.Open) + " closed"})
	}
	if state != "" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
		return out
	}
	for _, it := range s.Items() {
		if it.Milestone != "" && !seen[it.Milestone] && inAliases(it.Alias, aliases) {
			seen[it.Milestone] = true
			out = append(out, Candidate{it.Milestone, "milestone"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// Aliases lists the configured repo aliases.
func Aliases(s Source, typed string) []Candidate {
	var out []Candidate
	for _, a := range s.Aliases() {
		out = append(out, Candidate{a, "repo alias"})
	}
	return filterPrefix(out, typed, true)
}
