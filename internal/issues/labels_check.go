package issues

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// labelsFetched is how many labels `pull` asks GitHub for (see qIssues). A labels.tsv this long may be cut
// short, so it cannot prove that a label is missing.
const labelsFetched = 100

// repoLabels are the label names `pull` mirrored for a repo. ok is false when the list cannot vouch for
// absence: never pulled, or as long as one fetch can be.
func (a *App) repoLabels(short string) (names []string, ok bool) {
	s, err := readText(filepath.Join(a.Home, short, "labels.tsv"))
	if err != nil {
		return nil, false
	}
	for _, line := range splitLines(s) {
		if name, _, _ := strings.Cut(line, "\t"); name != "" {
			names = append(names, name)
		}
	}
	return names, len(names) < labelsFetched
}

// closeLabels are the mirrored labels a mistyped one most likely meant: ones it is a short name or suffix of
// (matchLabel), or within a couple of typos. At most five, in name order.
func closeLabels(want string, names []string) []string {
	seen := map[string]bool{}
	for _, l := range matchLabel(want, names) {
		seen[l] = true
	}
	limit := 2
	if len(want) <= 3 {
		limit = 1
	}
	lw := strings.ToLower(want)
	for _, l := range names {
		if editDistance(lw, strings.ToLower(l)) <= limit {
			seen[l] = true
		}
	}
	out := sortedKeys(seen)
	return out[:min(len(out), 5)]
}

// checkLabels refuses labels that do not exist on GitHub, using the mirrored labels.tsv, so a typo fails here
// instead of leaving a write stuck in the outbox that `sync` can never send. A label that differs from a real
// one only in case is rewritten to GitHub's spelling. moon never creates labels. When the repo's labels are
// unknown (local-only repo, never pulled, or a list that may be cut off) nothing is checked.
func (a *App) checkLabels(short string, labels []string) ([]string, error) {
	if len(labels) == 0 || a.localOnly(short) {
		return labels, nil
	}
	names, ok := a.repoLabels(short)
	if !ok {
		return labels, nil
	}
	out := make([]string, 0, len(labels))
	var bad []string
	for _, l := range labels {
		if c := exactLabel(l, names); c != "" {
			out = append(out, c)
			continue
		}
		msg := fmt.Sprintf("  %q", l)
		if near := closeLabels(l, names); len(near) > 0 {
			msg += " (did you mean " + strings.Join(near, ", ") + "?)"
		}
		bad = append(bad, msg)
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, die("%s has no such label, nothing was queued (moon does not create labels; add it on GitHub, then `moon issues pull`):\n%s", short, strings.Join(bad, "\n"))
	}
	return out, nil
}

// exactLabel is the mirrored name equal to l, ignoring case (GitHub label names are case-insensitive), or "".
func exactLabel(l string, names []string) string {
	for _, n := range names {
		if n == l {
			return n
		}
	}
	for _, n := range names {
		if strings.EqualFold(n, l) {
			return n
		}
	}
	return ""
}
