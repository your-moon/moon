package issues

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Terminal rendering for humans. Nothing here runs unless stdout is a terminal (or --format table
// is asked for), so piped output stays the byte-for-byte tab-separated text scripts rely on.

const defaultWidth = 100

// Colour roles used with the Styler (see color.go). cBold and cDim are the two that are not palette colours
// in name only: bold is an attribute, dim is the muted role.
const (
	cBold = "bold"
	cDim  = "muted"
)

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// ellipsize cuts s to at most n runes, ending in an ellipsis when it was cut.
func ellipsize(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

type col struct {
	head string
	max  int  // widest the column may get; 0 = unlimited
	flex bool // takes the leftover width (at most one per table)
}

// cell is one table cell: plain text s, painted in role color. render, when set and colour is on, draws the
// cell itself (label chips); it must print exactly the text of s.
type cell struct {
	s, color string
	render   func(st *Styler) string
}

func plain(s string) cell { return cell{s: s} }

// renderTable lays rows out in aligned columns, no wider than width. The flex column is truncated first.
func renderTable(cols []col, rows [][]cell, width int, st *Styler) string {
	const gap = 2
	if width <= 0 {
		width = defaultWidth
	}
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = runeLen(c.head)
		for _, r := range rows {
			if n := runeLen(r[i].s); n > w[i] {
				w[i] = n
			}
		}
		if c.max > 0 && w[i] > c.max && !c.flex {
			w[i] = c.max
		}
	}
	fixed := gap * (len(cols) - 1)
	flex := -1
	for i, c := range cols {
		if c.flex {
			flex = i
		} else {
			fixed += w[i]
		}
	}
	if flex >= 0 {
		room := width - fixed
		if room < 10 {
			room = 10
		}
		if room < w[flex] {
			w[flex] = room
		}
	}
	var b strings.Builder
	line := func(cells []cell, head bool) {
		for i, c := range cells {
			t := ellipsize(c.s, w[i])
			code := c.color
			if head {
				code = cBold
			}
			draw := st.P(code, t)
			if c.render != nil && t == c.s && !head && st != nil && st.On {
				draw = c.render(st)
			}
			if i == len(cells)-1 {
				b.WriteString(draw)
				break
			}
			b.WriteString(draw)
			b.WriteString(strings.Repeat(" ", w[i]-runeLen(t)+gap))
		}
		b.WriteString("\n")
	}
	hs := make([]cell, len(cols))
	for i, c := range cols {
		hs[i] = plain(c.head)
	}
	line(hs, true)
	for _, r := range rows {
		line(r, false)
	}
	return b.String()
}

// wrapLine breaks one line at spaces to fit width, keeping its indent on every piece.
func wrapLine(s string, width int) []string {
	if width < 20 || runeLen(s) <= width {
		return []string{s}
	}
	indent := s[:len(s)-len(strings.TrimLeft(s, " "))]
	words := strings.Fields(s)
	var out []string
	cur := indent
	for _, wd := range words {
		for runeLen(indent+wd) > width { // a word longer than the line is split
			room := width - runeLen(indent)
			if cur != indent {
				out = append(out, cur)
				cur = indent
			}
			r := []rune(wd)
			out = append(out, indent+string(r[:room]))
			wd = string(r[room:])
		}
		switch {
		case cur == indent:
			cur += wd
		case runeLen(cur)+1+runeLen(wd) <= width:
			cur += " " + wd
		default:
			out = append(out, cur)
			cur = indent + wd
		}
	}
	return append(out, cur)
}

// wrapText wraps every line to width; fenced code blocks and blank lines are left alone.
func wrapText(text string, width int, st *Styler) string {
	var out []string
	fence := false
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "```"):
			fence = !fence
			out = append(out, st.P(cDim, l))
		case fence:
			out = append(out, l)
		case strings.HasPrefix(l, "#"):
			out = append(out, st.P(cBold, l))
		default:
			out = append(out, wrapLine(l, width)...)
		}
	}
	return strings.Join(out, "\n")
}

func humanDur(sec int) string {
	switch {
	case sec < 0:
		sec = 0
		fallthrough
	case sec < 60:
		return fmt.Sprintf("%ds", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%dh%02dm", sec/3600, sec%3600/60)
	}
	return fmt.Sprintf("%dd", sec/86400)
}

func stateColor(st string) string {
	switch st {
	case "open":
		return "state-open"
	case "merged":
		return "state-merged"
	case "closed":
		return "state-closed"
	}
	return ""
}

// ---------- did you mean ----------

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// Closest is closest for other packages.
func Closest(word string, cands []string) string { return closest(word, cands) }

// closest returns the candidate nearest to word, or "" when nothing is near enough to be a typo.
func closest(word string, cands []string) string {
	word = strings.ToLower(strings.TrimLeft(word, "-"))
	sorted := append([]string{}, cands...)
	sort.Strings(sorted)
	best, bd := "", 1<<30
	for _, c := range sorted {
		name := strings.TrimLeft(c, "-")
		d := editDistance(word, name)
		if word != "" && strings.HasPrefix(name, word) && d > 1 {
			d = 1 // "mile" -> milestone
		}
		if d < bd {
			best, bd = c, d
		}
	}
	limit := 2
	if len(word) <= 3 {
		limit = 1
	}
	if bd > limit {
		return ""
	}
	return best
}
