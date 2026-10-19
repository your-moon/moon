package issues

import "strings"

// Reads show the mirror with the queued writes laid over it, so work that has not synced yet (a close, a label
// change, an edit, a milestone move) is visible at once. Every reader of the index goes through readIndex, so
// ls, frontier, claims, milestones, status and the TUI see the same rows.

// overlayOps is the queued writes that count for reads: all of them, in queue order, except the ones that
// failed their last send. A failed op stays in the outbox for repair (`outbox ls|edit|drop`) but is not shown
// as done.
func (a *App) overlayOps() []*Op { return effectiveOps(a.loadOutbox()) }

// effectiveOps is ops without the ones that failed their last send: the writes every reader of issue state
// (ls, show, show --json, the TUI) applies. Raw loadOutbox stays for the outbox commands, push and counts.
func effectiveOps(ops []*Op) []*Op {
	var out []*Op
	for _, op := range ops {
		if op.LastError == nil {
			out = append(out, op)
		}
	}
	return out
}

// overlayIndex applies the queued writes of the issues in rows (close, reopen, label, milestone, edit) in queue
// order. Rows without a queued write are returned untouched.
func (a *App) overlayIndex(short string, rows [][]string) [][]string {
	if len(rows) == 0 {
		return rows
	}
	byRef := map[string][]*Op{}
	for _, op := range a.overlayOps() {
		if op.Op != "new" && shortOf(op.Ref) == short {
			byRef[op.Ref] = append(byRef[op.Ref], op)
		}
	}
	if len(byRef) == 0 {
		return rows
	}
	out := make([][]string, len(rows))
	for i, r := range rows {
		ops := byRef[short+"#"+r[0]]
		if len(ops) == 0 {
			out[i] = r
			continue
		}
		e := &eff{title: r[5], state: r[1], milestone: r[2], labels: splitLabels(r[3])}
		applyPending(e, ops)
		nr := append([]string(nil), r...)
		nr[1], nr[2], nr[3], nr[5] = e.state, e.milestone, strings.Join(e.labels, ","), clean(e.title)
		out[i] = nr
	}
	return out
}
