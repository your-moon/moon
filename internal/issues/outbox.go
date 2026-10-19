package issues

import (
	"fmt"
	"strings"
)

// moon issues outbox: look at the queued writes, fix one that cannot be sent, or drop it. Before this the only
// repair for a write stuck in sync (a bad label on a `new`, say) was editing the JSON under <mirror>/outbox.

// withQueueLock runs fn while no push is running, so a drop or edit cannot race the send of the same op.
func (a *App) withQueueLock(fn func() error) error {
	f, ok := a.mirror().tryPushLock()
	if !ok {
		return die1("a push is running; try again in a moment")
	}
	defer f.Close()
	return fn()
}

// findOp resolves what a person typed to one queued op: the whole op id, or any piece of it that only one op
// has (op ids are long; `outbox ls` shows the last 8 characters).
func (a *App) findOp(typed string) (*Op, error) {
	if len(typed) < 4 {
		return nil, die("op id %q is too short: give at least 4 characters of one from `moon issues outbox ls`", typed)
	}
	var hits []*Op
	for _, op := range a.loadOutbox() {
		if op.OpID == typed {
			return op, nil
		}
		if strings.Contains(op.OpID, typed) {
			hits = append(hits, op)
		}
	}
	switch len(hits) {
	case 0:
		return nil, die1("no queued op matches %q (see: moon issues outbox ls)", typed)
	case 1:
		return hits[0], nil
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = shortOpID(h.OpID)
	}
	return nil, die("%q matches %d ops (%s); give more of the id", typed, len(hits), strings.Join(ids, ", "))
}

func shortOpID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// outboxSummary is one line about what an op will do.
func outboxSummary(op *Op) string {
	if op.Op == "new" {
		s := "new: " + argStr(op.Args, "title")
		if l := argList(op.Args, "labels"); len(l) > 0 {
			s += " [" + strings.Join(l, ",") + "]"
		}
		return s
	}
	return describe(op)
}

func (a *App) cmdOutboxLs(o *Opts) error {
	v := a.resolveView(o, false)
	var ops []*Op
	for _, op := range a.loadOutbox() {
		if a.inScope(shortOf(op.Ref)) {
			ops = append(ops, op)
		}
	}
	errText := func(op *Op) string {
		if op.LastError != nil {
			return *op.LastError
		}
		return ""
	}
	switch v.format {
	case "json":
		l := []any{}
		for _, op := range ops {
			l = append(l, Obj{{"op_id", op.OpID}, {"op", op.Op}, {"ref", op.Ref}, {"attempts", op.Attempts}, {"summary", outboxSummary(op)},
				{"last_error", nilIfEmpty(errText(op))}, {"args", op.Args}})
		}
		a.println(dumps(l, false))
	case "table":
		if len(ops) == 0 {
			fmt.Fprintln(a.Err, "outbox is empty")
			return nil
		}
		cells := make([][]cell, len(ops))
		for i, op := range ops {
			e := errText(op)
			cells[i] = []cell{plain(shortOpID(op.OpID)), plain(op.Op), {s: op.Ref, color: cBold}, plain(fmt.Sprint(op.Attempts)),
				plain(oneLine(outboxSummary(op))), {s: oneLine(e), color: "error"}}
		}
		cols := []col{{head: "ID"}, {head: "OP"}, {head: "REF"}, {head: "TRIES"}, {head: "WRITE", max: 50}, {head: "LAST ERROR", max: 60}}
		fmt.Fprint(a.Out, renderTable(cols, cells, a.Width, a.styler()))
	default:
		for _, op := range ops {
			a.printf("%s\t%s\t%s\t%d\t%s\t%s\n", op.OpID, op.Op, op.Ref, op.Attempts, oneLine(outboxSummary(op)), orDash(oneLine(errText(op))))
		}
	}
	return nil
}

// dependents are the queued ops that act on the issue a queued `new` will create; once the create is gone they
// could never be sent ("unresolved local ref").
func (a *App) dependents(op *Op) []*Op {
	var out []*Op
	if op.Op != "new" {
		return nil
	}
	for _, p := range a.loadOutbox() {
		if p.OpID != op.OpID && p.Ref == op.Ref {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) cmdOutboxDrop(o *Opts) error {
	op, err := a.findOp(o.Pos[0])
	if err != nil {
		return err
	}
	deps := a.dependents(op)
	if len(deps) > 0 && !o.B["cascade"] {
		return die("%s creates %s, and %d queued write(s) act on it; dropping the create would strand them. Use --cascade to drop them too (see: moon issues outbox ls)", shortOpID(op.OpID), op.Ref, len(deps))
	}
	return a.withQueueLock(func() error {
		for _, d := range append(deps, op) {
			if err := a.mirror().removeOp(d.File); err != nil && !isNotExist(err) {
				return err
			}
			a.printf("dropped %s  %s %s\n", d.OpID, d.Op, d.Ref)
		}
		return nil
	})
}

// permanentPrefix starts the last error of an op that sync gave up on.
const permanentPrefix = "permanent, no retry: "

// permanentMarkers are the GitHub errors a retry can never fix.
var permanentMarkers = []string{
	"has disabled issues",
	"issues are disabled",
	"Could not resolve to a Repository",
	"repository has been archived",
	"repository is archived",
}

func permanentError(msg string) bool {
	l := strings.ToLower(msg)
	for _, m := range permanentMarkers {
		if strings.Contains(l, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

func (a *App) cmdOutboxRetry(o *Opts) error {
	op, err := a.findOp(o.Pos[0])
	if err != nil {
		return err
	}
	return a.withQueueLock(func() error {
		op.Permanent, op.LastError, op.Attempts = false, nil, 0
		if err := a.mirror().saveOp(op); err != nil {
			return err
		}
		a.printf("retry %s  %s %s\n", op.OpID, op.Op, op.Ref)
		return nil
	})
}
