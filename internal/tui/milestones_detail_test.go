package tui

import (
	"strings"
	"testing"
)

func TestMilestonesTabShowsDueAndDescription(t *testing.T) {
	mi := newMirror(t, base)
	mi.milestones("app",
		"1\t\talpha\topen\t2026-03-10\tship the first cut\\nsecond line\t2026-03-01T00:00:00Z",
		"2\t\tbeta\topen\t\t\t2026-03-01T00:00:00Z")
	for _, w := range []int{100, 50} {
		s := sdo(mi.shell(w, 20), "2")
		v := s.View()
		if w == 100 && (!strings.Contains(v, "ship the first cut second line") || !strings.Contains(v, "2026-03-10")) {
			t.Errorf("w=%d: due column or description missing:\n%s", w, v)
		}
		if w == 50 && !strings.Contains(v, "due 2026-03-10: ship the first") {
			t.Errorf("w=%d: due date missing on a narrow screen:\n%s", w, v)
		}
		s = sdo(s, "j") // beta has neither
		if strings.Contains(s.View(), "ship the first cut") {
			t.Errorf("w=%d: description of another row is shown", w)
		}
	}
}
