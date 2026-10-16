package issues

import (
	"sort"
	"strconv"
	"strings"
)

// Label display. A label is shown by its stored name ("full") or by a short name ("short"): the kind:, area:
// and triage: prefixes dropped and double colons collapsed. Whatever is shown, -L accepts it.

const (
	LabelsFull  = "full"
	LabelsShort = "short"
	LabelsNone  = "none"
)

func labelGroup(l string) int {
	switch {
	case strings.HasPrefix(l, "kind:"):
		return 0
	case strings.HasPrefix(l, "area:"):
		return 1
	case strings.HasPrefix(l, "triage:"):
		return 2
	}
	return 3
}

// OrderLabels puts kind:* first, then area:*, then triage:*, then the rest alphabetical. Within a group the
// stored order is kept (the rest is sorted).
func OrderLabels(labels []string) []string {
	out := append([]string{}, labels...)
	sort.SliceStable(out, func(i, j int) bool {
		gi, gj := labelGroup(out[i]), labelGroup(out[j])
		if gi != gj {
			return gi < gj
		}
		return gi == 3 && strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// collapseColons turns "area::auth" into "area:auth".
func collapseColons(l string) string {
	for strings.Contains(l, "::") {
		l = strings.ReplaceAll(l, "::", ":")
	}
	return l
}

// LabelShort is the display name of a label in short mode.
func LabelShort(l string) string {
	c := collapseColons(l)
	for _, p := range []string{"kind:", "area:", "triage:"} {
		if strings.HasPrefix(c, p) && len(c) > len(p) {
			return c[len(p):]
		}
	}
	return c
}

// LabelModeFor is the default labels mode of a terminal this wide: full from 110 columns, short from 70, else none.
func LabelModeFor(width int) string {
	switch {
	case width >= 110:
		return LabelsFull
	case width >= 70:
		return LabelsShort
	}
	return LabelsNone
}

// LabelNames are the labels as displayed in the mode, in importance order.
func LabelNames(labels []string, mode string) []string {
	var out []string
	for _, l := range OrderLabels(labels) {
		if mode == LabelsShort {
			l = LabelShort(l)
		}
		out = append(out, l)
	}
	return out
}

// LabelsText is all labels, comma-joined, never cut.
func LabelsText(labels []string, mode string) string {
	if len(labels) == 0 {
		return "-"
	}
	return strings.Join(LabelNames(labels, mode), ",")
}

// FitLabelParts picks the labels that fit in w columns: the display names, the stored names they stand for,
// and how many were dropped. Whole labels are dropped from the end; one is never cut mid-name.
func FitLabelParts(labels []string, mode string, w int) (shown, stored []string, more int) {
	stored = OrderLabels(labels)
	names := LabelNames(labels, mode)
	if len(names) == 0 {
		return nil, nil, 0
	}
	if runeLen(strings.Join(names, ",")) <= w {
		return names, stored, 0
	}
	for k := len(names) - 1; k >= 1; k-- {
		if runeLen(strings.Join(names[:k], ",")+" +"+strconv.Itoa(len(names)-k)) <= w {
			return names[:k], stored[:k], len(names) - k
		}
	}
	return nil, nil, len(names)
}

// FitLabels is the labels within w columns as text: "kind:bug,area:web +2".
func FitLabels(labels []string, mode string, w int) string {
	shown, _, more := FitLabelParts(labels, mode, w)
	if len(labels) == 0 {
		return "-"
	}
	s := strings.Join(shown, ",")
	switch {
	case more == 0:
		return s
	case s == "":
		return "+" + strconv.Itoa(more)
	}
	return s + " +" + strconv.Itoa(more)
}

// LabelColWidth is the width of the labels column: the widest full label text, at least the header, at most
// 45 percent of the terminal.
func LabelColWidth(texts []string, header string, termWidth int) int {
	w := runeLen(header)
	for _, t := range texts {
		w = max(w, runeLen(t))
	}
	return min(w, max(termWidth*45/100, runeLen(header)))
}
