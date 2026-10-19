package completion

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/your-moon/moon/internal/issues"
)

// This file is the in-process entry points the dashboard calls. They share every candidate source with
// `moon __complete` (Refs, Labels, Milestones, Aliases, RefFilter, Complete); nothing here has its own data.

// Word is one word of a typed line: Text is unquoted, [Start, End) is its raw span in the line (bytes).
type Word struct {
	Text       string
	Start, End int
}

// SplitWords splits text like a shell would (quotes, backslash). When the text ends in a space, or is empty,
// the last word is empty and sits at the end. The returned quote is the one left open in the last word.
func SplitWords(text string) (words []Word, quote byte) {
	var cur strings.Builder
	start, in := 0, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(text) && strings.IndexByte("\"\\", text[i+1]) >= 0:
				i++
				cur.WriteByte(text[i])
			default:
				cur.WriteByte(c)
			}
		case c == ' ' || c == '\t':
			if in {
				words = append(words, Word{cur.String(), start, i})
				cur.Reset()
				in = false
			}
		default:
			if !in {
				in, start = true, i
			}
			switch {
			case c == '"' || c == '\'':
				quote = c
			case c == '\\' && i+1 < len(text):
				i++
				cur.WriteByte(text[i])
			default:
				cur.WriteByte(c)
			}
		}
	}
	if in {
		words = append(words, Word{cur.String(), start, len(text)})
	} else {
		words = append(words, Word{"", len(text), len(text)})
	}
	return words, quote
}

// QuoteWord wraps s in double quotes when it has a space, quote or backslash.
func QuoteWord(s string) string {
	if s == "" || !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// CommonPrefix is the longest prefix shared by all values.
func CommonPrefix(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	p := vals[0]
	for _, v := range vals[1:] {
		for !strings.HasPrefix(v, p) {
			p = p[:len(p)-1]
			for !utf8.ValidString(p) {
				p = p[:len(p)-1]
			}
		}
	}
	return p
}

// FilterKeys are the tokens the search box understands: label:bug milestone:"v1 beta" status:claimed repo:erp.
var FilterKeys = []Candidate{
	{"label:", "only issues with this label (stored or short name)"},
	{"milestone:", "only issues in this milestone"},
	{"status:", "working, in review, stale, blocked, ready, open or done"},
	{"repo:", "only this repo alias"},
}

// StatusValues are the work statuses, the values of the status: filter (and of --status in the shell).
var StatusValues = []Candidate{
	{"working", "a live claim"}, {"in review", "an open PR closes it"}, {"stale", "the claim expired or its process died"},
	{"blocked", "labelled blocked"}, {"ready", "labelled ready, nobody on it"}, {"open", "open, nobody on it"}, {"done", "closed"},
}

// Query is a search box text split into filter tokens and free text.
type Query struct {
	Text       string // free text: substring of title or labels, as ls -q
	Labels     []string
	Milestone  string
	Status     string
	Repo       string
	HasFilters bool
}

// ParseQuery recognizes label:X milestone:X status:X repo:X (X may be quoted). A text without any of those is
// returned unchanged as free text, so plain searches behave exactly as before.
func ParseQuery(q string) Query {
	words, _ := SplitWords(q)
	isKey := func(w Word) (string, string, bool) {
		for _, k := range FilterKeys {
			if strings.HasPrefix(w.Text, k.Value) && strings.HasPrefix(q[w.Start:], k.Value[:len(k.Value)-1]) {
				return k.Value[:len(k.Value)-1], w.Text[len(k.Value):], true
			}
		}
		return "", "", false
	}
	any := false
	for _, w := range words {
		if _, _, ok := isKey(w); ok {
			any = true
		}
	}
	if !any {
		return Query{Text: q}
	}
	out := Query{HasFilters: true}
	var rest []string
	for _, w := range words {
		k, v, ok := isKey(w)
		if !ok {
			if w.Text != "" {
				rest = append(rest, q[w.Start:w.End])
			}
			continue
		}
		switch k {
		case "label":
			out.Labels = append(out.Labels, v)
		case "milestone":
			out.Milestone = v
		case "status":
			out.Status = v
		case "repo":
			out.Repo = v
		}
	}
	out.Text = strings.Join(rest, " ")
	return out
}

// Match reports whether an item passes the filter tokens of the query (free text is the caller's).
func (q Query) Match(it *issues.Item) bool {
	for _, want := range q.Labels { // the same resolution as ls -L: stored name, shown short name, or suffix
		if len(issues.MatchLabel(want, it.Labels)) == 0 {
			return false
		}
	}
	if q.Milestone != "" && !strings.EqualFold(it.Milestone, q.Milestone) && !(q.Milestone == "none" && it.Milestone == "") {
		return false
	}
	if q.Repo != "" && it.Alias != q.Repo {
		return false
	}
	if q.Status != "" {
		ok := false
		for _, w := range strings.Split(q.Status, ",") {
			ok = ok || issues.NormStatus(w) == it.Work.Name
		}
		if !ok {
			return false
		}
	}
	return true
}

func tokenValues(s Source, key, typed string) []Candidate {
	var c []Candidate
	switch key {
	case "label":
		c = Labels(s, nil)
	case "milestone":
		c = Milestones(s, nil)
	case "status":
		c = StatusValues
	case "repo":
		c = Aliases(s, "")
	}
	return filterPrefix(c, typed, true)
}

// wordAt finds the word under it. Replacement candidates cover [start, end) of text.
func wordAt(text string, cursor int) (w Word, typed string) {
	words, _ := SplitWords(text)
	found := false
	for _, x := range words {
		if x.Start <= cursor && cursor <= x.End {
			w, found = x, true
			break
		}
	}
	if !found {
		w = Word{Start: cursor, End: cursor}
	}
	tw, _ := SplitWords(text[w.Start:cursor])
	return w, tw[len(tw)-1].Text
}

// CompleteSearch completes the word under the cursor (a byte offset) of the search box: refs, label names
// (stored and short), milestone titles, and the values of label: milestone: status: repo: tokens. It
// returns the span to replace and candidates whose Value is ready to be put in that span.
func CompleteSearch(s Source, text string, cursor int) (start, end int, cands []Candidate) {
	w, typed := wordAt(text, cursor)
	start, end = w.Start, w.End
	if i := strings.IndexByte(typed, ':'); i > 0 {
		key := typed[:i]
		for _, k := range FilterKeys {
			if k.Value == key+":" {
				for _, c := range tokenValues(s, key, typed[i+1:]) {
					cands = append(cands, Candidate{key + ":" + QuoteWord(c.Value), c.Desc})
				}
				return
			}
		}
	}
	cands = append(cands, filterPrefix(FilterKeys, typed, false)...)
	if typed != "" {
		for _, c := range Refs(s, "show", typed, nil) {
			cands = append(cands, c)
		}
		cands = append(cands, filterPrefix(Labels(s, nil), typed, true)...)
		cands = append(cands, filterPrefix(Milestones(s, nil), typed, true)...)
	} else {
		cands = append(cands, Labels(s, nil)...)
		cands = append(cands, Milestones(s, nil)...)
	}
	for i := range cands {
		if !strings.HasSuffix(cands[i].Value, ":") {
			cands[i].Value = QuoteWord(cands[i].Value)
		}
	}
	cands = dedupe(cands)
	return
}

func dedupe(c []Candidate) []Candidate {
	seen := map[string]bool{}
	out := c[:0:0]
	for _, x := range c {
		if !seen[x.Value] {
			seen[x.Value] = true
			out = append(out, x)
		}
	}
	return out
}

// LineCommands are the commands of the dashboard's ":" line.
// The item actions come from internal/issues/actions.go; the rest are the view's own.
var LineCommands = lineCommands()

func lineCommands() []Candidate {
	var c []Candidate
	for _, a := range issues.Actions() {
		c = append(c, Candidate{a.Name, a.Help})
	}
	return append(c, []Candidate{
		{"sort", "number, updated, created, title; add reverse"}, {"filter", "state:open label:bug milestone:v1 repo:erp, or clear"},
		{"repo", "show one repo alias (all clears)"}, {"q", "quit"},
	}...)
}

var sortValues = []Candidate{{"number", ""}, {"updated", ""}, {"created", ""}, {"title", ""}, {"reverse", "reverse the order"}}

// CompleteLine completes the ":" command line. Arguments are completed by the same engine as the shell
// (Complete), so claim offers open issues not held live by someone else, release only claimed ones, reopen only closed ones.
func CompleteLine(s Source, text string, cursor int) (start, end int, cands []Candidate) {
	w, typed := wordAt(text, cursor)
	start, end = w.Start, w.End
	words, _ := SplitWords(text[:cursor])
	done := words[:len(words)-1]
	quoteAll := func(c []Candidate) []Candidate {
		for i := range c {
			c[i].Value = QuoteWord(c[i].Value)
		}
		return c
	}
	if len(done) == 0 {
		return start, end, filterPrefix(LineCommands, typed, false)
	}
	cmd := done[0].Text
	args := make([]string, 0, len(done))
	for _, d := range done[1:] {
		args = append(args, d.Text)
	}
	if _, isAction := issues.ActionNamed(cmd); (isAction && cmd != "label") || cmd == "show" { // the ref comes first
		if strings.HasPrefix(typed, "-") {
			return
		}
		all := append(append([]string{"issues", cmd}, args...), typed)
		r := Complete(Options{Open: func(string, string) Source { return s }}, all)
		return start, end, quoteAll(r.Cands)
	}
	switch cmd {
	case "label":
		switch len(args) {
		case 0:
			return start, end, quoteAll(Refs(s, "label", typed, nil))
		case 1:
			return start, end, filterPrefix([]Candidate{{"add", "add a label"}, {"remove", "remove a label"}}, typed, false)
		default:
			c := Labels(s, nil)
			if args[1] == "remove" {
				c = labelsOf(s, args[0])
			}
			return start, end, quoteAll(filterPrefix(c, typed, true))
		}
	case "sort":
		if len(args) < 2 {
			return start, end, filterPrefix(sortValues, typed, false)
		}
	case "filter":
		if typed == "" || !strings.Contains(typed, ":") {
			c := filterPrefix(append([]Candidate{{"state:", "open, closed or all"}, {"clear", "drop every filter"}}, FilterKeys[:2]...), typed, false)
			c = append(c, filterPrefix(FilterKeys[3:], typed, false)...)
			return start, end, c
		}
		i := strings.IndexByte(typed, ':')
		key := typed[:i]
		var vals []Candidate
		if key == "state" {
			vals = filterPrefix([]Candidate{{"open", ""}, {"closed", ""}, {"all", ""}}, typed[i+1:], true)
		} else {
			vals = tokenValues(s, key, typed[i+1:])
		}
		for _, c := range vals {
			cands = append(cands, Candidate{key + ":" + QuoteWord(c.Value), c.Desc})
		}
		return
	case "repo":
		if len(args) == 0 {
			return start, end, filterPrefix(append(Aliases(s, ""), Candidate{"all", "show every repo"}), typed, true)
		}
	}
	return
}

func labelsOf(s Source, ref string) []Candidate {
	for _, it := range s.Items() {
		if it.Ref == ref {
			var out []Candidate
			for _, l := range it.Labels {
				out = append(out, Candidate{l, "on " + ref})
			}
			sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
			return out
		}
	}
	return nil
}

// Narrow keeps the candidates that contain typed, ignoring case: the type-ahead of the pickers.
func Narrow(c []Candidate, typed string) []Candidate {
	if typed == "" {
		return c
	}
	t := strings.ToLower(typed)
	var out []Candidate
	for _, x := range c {
		if strings.Contains(strings.ToLower(x.Value), t) {
			out = append(out, x)
		}
	}
	return out
}
