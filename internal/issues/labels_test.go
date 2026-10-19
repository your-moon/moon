package issues

import (
	"reflect"
	"strings"
	"testing"
)

func TestOrderLabels(t *testing.T) {
	in := []string{"zeta", "triage:ready", "area:web", "kind:bug", "alpha", "kind::feature", "area::api", "Beta"}
	want := []string{"kind:bug", "kind::feature", "area:web", "area::api", "triage:ready", "alpha", "Beta", "zeta"}
	if got := OrderLabels(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if in[0] != "zeta" {
		t.Error("input was reordered")
	}
}

func TestShortNames(t *testing.T) {
	for in, want := range map[string]string{"kind:bug": "bug", "kind::feature": "feature", "area:ui": "ui", "area::auth": "auth", "triage:needs-decision": "needs-decision",
		"scope::x": "scope:x", "wontfix": "wontfix", "kind:": "kind:"} {
		if got := LabelShort(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestFitLabelsDropsWholeLabels(t *testing.T) {
	labels := []string{"wontfix", "triage:ready", "kind:bug", "zzz-long-label-name", "area:web"} // 58 columns in importance order
	full := "kind:bug,area:web,triage:ready,wontfix,zzz-long-label-name"
	for _, c := range []struct {
		w    int
		want string
	}{
		{80, full}, {58, full},
		{57, "kind:bug,area:web,triage:ready,wontfix +1"},
		{41, "kind:bug,area:web,triage:ready,wontfix +1"},
		{40, "kind:bug,area:web,triage:ready +2"},
		{33, "kind:bug,area:web,triage:ready +2"},
		{32, "kind:bug,area:web +3"},
		{20, "kind:bug,area:web +3"},
		{19, "kind:bug +4"},
		{11, "kind:bug +4"},
		{10, "+5"},
		{2, "+5"},
	} {
		got := FitLabels(labels, LabelsFull, c.w)
		if got != c.want || runeLen(got) > max(c.w, 2) {
			t.Errorf("w=%d: %q want %q", c.w, got, c.want)
		}
		// never cut mid-name: every shown piece is a whole label
		for _, p := range strings.Split(strings.SplitN(got, " +", 2)[0], ",") {
			if p != "" && p != "+5" && !contains(labels, p) {
				t.Errorf("w=%d: %q is not a whole label", c.w, p)
			}
		}
	}
	if got := FitLabels(nil, LabelsFull, 10); got != "-" {
		t.Errorf("none: %q", got)
	}
	if got := FitLabels(labels, LabelsShort, 80); got != "bug,web,ready,wontfix,zzz-long-label-name" {
		t.Errorf("short: %q", got)
	}
}

func TestLabelColWidth(t *testing.T) {
	texts := []string{"kind:bug", "kind:feature,area:payments-gateway"}
	if got := LabelColWidth(texts, "LABELS", 200); got != runeLen(texts[1]) {
		t.Errorf("fits: %d", got)
	}
	if got := LabelColWidth(texts, "LABELS", 60); got != 27 { // 45% of 60
		t.Errorf("capped: %d", got)
	}
	if got := LabelColWidth([]string{"a"}, "LABELS", 200); got != 6 {
		t.Errorf("header minimum: %d", got)
	}
	if got := LabelColWidth(nil, "LABELS", 10); got != 6 {
		t.Errorf("tiny terminal never goes under the header: %d", got)
	}
	for w, want := range map[int]string{200: LabelsFull, 110: LabelsFull, 109: LabelsShort, 70: LabelsShort, 69: LabelsNone, 40: LabelsNone} {
		if got := LabelModeFor(w); got != want {
			t.Errorf("mode at %d: %s want %s", w, got, want)
		}
	}
}

const longLabels = "kind:bug,area:payments-gateway,triage:needs-information,priority-high,customer-reported"

func longFixture(e *env) {
	e.putFile("app", fileIndex, strings.Join([]string{
		"3\topen\t\t" + longLabels + "\t2026-02-12T09:00:00Z\tcheckout: card form loses focus after the 3-D secure redirect on slow phones",
		"2\topen\t\tkind:bug,area:web\t2026-02-11T09:00:00Z\tsearch: slow on big projects",
		"1\topen\t\t\t2026-02-10T09:00:00Z\tdocs: no labels at all",
		"",
	}, "\n"))
	e.putFile("app", fileLastpull, "1772366400\n")
}

// column returns where a header starts in the first line.
func column(table, head string) int { return strings.Index(strings.Split(table, "\n")[0], head) }

func TestLabelsColumnSizing(t *testing.T) {
	ordered := "kind:bug,area:payments-gateway,triage:needs-information,priority-high,customer-reported" // already in importance order
	for _, c := range []struct {
		width int
		mode  string
		wantW int // width of the labels column, 0 = no column
	}{
		{160, LabelsFull, runeLen(ordered)}, // 87 columns of labels, but the cap is 45% of 160 = 72
		{120, LabelsFull, 54},               // cap 54
		{80, LabelsShort, 0},                // computed below: the short text fits under the cap of 36
		{60, LabelsNone, 0},
	} {
		e := newEnv(t)
		longFixture(e)
		e.tty, e.width, e.extra["NO_COLOR"] = true, c.width, "1"
		out, _, code := e.run("ls", "--repo", "app")
		if code != 0 {
			t.Fatal(code)
		}
		lines := strings.Split(out, "\n")
		switch c.mode {
		case LabelsNone:
			if strings.Contains(out, "LABELS") || strings.Contains(out, "+") {
				t.Errorf("w=%d: labels should be hidden:\n%s", c.width, out)
			}
			continue
		case LabelsFull:
			cap := c.width * 45 / 100
			gap := column(out, "TITLE") - column(out, "LABELS") - 2
			want := FitLabels(strings.Split(longLabels, ","), LabelsFull, cap)
			// as wide as the widest row needs, never wider than the cap: whole labels dropped leave the column
			// as wide as the longest text that was kept
			if gap != runeLen(want) || gap > cap || (c.width == 160 && runeLen(ordered) <= cap) {
				t.Errorf("w=%d: labels column is %d wide, want %d (cap %d)\n%s", c.width, gap, runeLen(want), cap, out)
			}
			if !strings.Contains(lines[1], want) || !strings.Contains(want, " +") {
				t.Errorf("w=%d: row 1 should hold %q:\n%s", c.width, want, lines[1])
			}
		case LabelsShort:
			shortText := LabelsText(strings.Split(longLabels, ","), LabelsShort)
			if !strings.Contains(lines[1], shortText) && !strings.Contains(lines[1], " +") {
				t.Errorf("w=%d: short labels missing:\n%s", c.width, lines[1])
			}
			if strings.Contains(out, "kind:bug") {
				t.Errorf("w=%d: short mode shows prefixes:\n%s", c.width, out)
			}
		}
		// the title is the column that gives way, never a label cut in the middle
		for _, l := range lines[1:4] {
			if l == "" {
				continue
			}
			if idx := strings.Index(l, "…"); idx >= 0 && idx < column(out, "TITLE") {
				t.Errorf("w=%d: a cut before the title column: %q", c.width, l)
			}
		}
		if !strings.Contains(lines[1], "checkout: card form") {
			t.Errorf("w=%d: title lost:\n%s", c.width, lines[1])
		}
	}
}

func TestLabelsFlagModes(t *testing.T) {
	e := newEnv(t)
	longFixture(e)
	e.tty, e.width, e.extra["NO_COLOR"] = true, 120, "1"
	full, _, _ := e.run("ls", "--repo", "app", "-B", "full")
	short, _, _ := e.run("ls", "--repo", "app", "-B", "short")
	none, _, _ := e.run("ls", "--repo", "app", "-B", "none")
	if !strings.Contains(full, "kind:bug,area:web") || !strings.Contains(short, "bug,web") || strings.Contains(short, "kind:") || strings.Contains(none, "LABELS") {
		t.Errorf("modes:\nfull:\n%s\nshort:\n%s\nnone:\n%s", full, short, none)
	}
	if l, _, _ := e.run("ls", "--repo", "app", "--labels", "short"); l != short {
		t.Error("--labels is -B")
	}
	// at 60 columns the default is none, -B forces labels back
	e.width = 60
	if out, _, _ := e.run("ls", "--repo", "app"); strings.Contains(out, "LABELS") {
		t.Errorf("60 columns default:\n%s", out)
	}
	if out, _, _ := e.run("ls", "--repo", "app", "-B", "short"); !strings.Contains(out, "LABELS") {
		t.Errorf("-B short at 60:\n%s", out)
	}
	if _, errs, code := e.run("ls", "-B", "huge"); code != 2 || !strings.Contains(errs, "invalid choice") {
		t.Errorf("bad mode: %d %q", code, errs)
	}
	// what is shown is what -L takes
	for _, shown := range []string{"bug", "kind:bug", "web", "area:web", "payments-gateway", "needs-information"} {
		out, _, code := e.run("ls", "--repo", "app", "-L", shown, "--format", "tsv")
		if code != 0 || len(refsOf(out)) == 0 {
			t.Errorf("-L %s matched nothing (%d): %q", shown, code, out)
		}
	}
	// piped output is the stored order, untouched
	piped, _, _ := e.run("ls", "--repo", "app", "--format", "tsv")
	if !strings.Contains(piped, "\t"+longLabels+"\t") {
		t.Errorf("tsv labels changed:\n%s", piped)
	}
}

func TestShowListsEveryLabelInFull(t *testing.T) {
	e := newEnv(t)
	longFixture(e)
	n := fixtureIssue()
	n.Number = 3
	n.Labels.Nodes = nil
	for _, l := range strings.Split(longLabels, ",") {
		n.Labels.Nodes = append(n.Labels.Nodes, struct {
			Name string `json:"name"`
		}{l})
	}
	e.putIssue("app", "3", renderIssue(n))
	e.tty, e.width, e.extra["NO_COLOR"] = true, 40, "1"
	out, _, code := e.run("show", "app#3")
	if code != 0 {
		t.Fatal(code)
	}
	var block []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "labels:") || (len(block) > 0 && strings.HasPrefix(l, "       ")) {
			block = append(block, l)
		}
	}
	if len(block) < 2 {
		t.Fatalf("labels should wrap at 40 columns:\n%s", out)
	}
	joined := strings.Join(block, " ")
	for _, l := range strings.Split(longLabels, ",") {
		if !strings.Contains(joined, l) {
			t.Errorf("label %q is missing or cut:\n%s", l, strings.Join(block, "\n"))
		}
	}
	for _, l := range block {
		if runeLen(l) > 40 || strings.Contains(l, "…") || strings.Contains(l, " +") {
			t.Errorf("label line cut or too wide: %q", l)
		}
	}
	if !strings.HasPrefix(block[0], "labels: kind:bug,") {
		t.Errorf("importance order: %q", block[0])
	}
}
