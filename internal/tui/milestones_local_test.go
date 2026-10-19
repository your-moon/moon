package tui

import (
	"strings"
	"testing"
)

const queuedMs = `{"op_id": "20260301T120000.000000-1-aaaa", "ts": "2026-03-01T12:00:00", "op": "ms_new", "ref": "erp#M-1", "args": {"title": "Local One", "description": "", "due": "", "local_id": "M-1"}, "attempts": 0, "last_error": null}` + "\n"

// A milestone that was only created locally (still queued) is offered by the picker and by :milestone,
// and :milestone accepts its M-<n> id.
func TestLocalMilestonesInPickerAndLine(t *testing.T) {
	m, mi := compModel(t, 120, 30)
	mi.queueOp("20260301T120000.000000-1-aaaa", queuedMs)
	m = press(m, "r") // reload
	m = mi.model(120, 30)
	m = press(m, "j", "M")
	eq(t, "picker lists titles, local ones too, no ids", strings.Join(m.pickOpts, "|"), "|Local One|v1|v1 beta")
	m = press(m, "esc", ":")
	m = typed(m, "milestone erp#51 M")
	m = press(m, "tab")
	if !strings.Contains(cands(m)+" "+m.input.Value(), "M-1") {
		t.Fatalf("the line should offer M-1: cands=%q input=%q", cands(m), m.input.Value())
	}
	m = press(m, "esc", ":")
	m = typed(m, "milestone erp#51 M-1")
	m = press(m, "enter")
	if !strings.Contains(m.msg, "queued milestone") || mi.outbox() != 2 {
		t.Fatalf("msg=%q outbox=%d", m.msg, mi.outbox())
	}
	var body string
	for _, op := range mi.queuedOps() {
		if strings.Contains(op, `"op": "milestone"`) {
			body = op
		}
	}
	if !strings.Contains(body, `"title": "Local One"`) {
		t.Fatalf("M-1 must be resolved to the title in the queued op: %s", body)
	}
}
