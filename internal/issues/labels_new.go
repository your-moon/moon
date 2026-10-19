package issues

import (
	"fmt"
	"regexp"
	"strings"
)

// moon issues labels new: a label is created like a milestone is, queued locally and sent by sync before the
// writes that use it (msWaiting holds those back). checkLabels accepts a label with a queued create, so
// `new -L` and `label --add` work offline for it.

var labelColorRe = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

func isLabelSub(w string) bool { return w == "new" }

// queuedLabels are the names of labels whose creation is queued for a repo.
func (a *App) queuedLabels(short string) []string {
	var out []string
	for _, op := range a.overlayOps() {
		if op.Op == "label_new" && shortOf(op.Ref) == short {
			out = append(out, argStr(op.Args, "name"))
		}
	}
	return out
}

func (a *App) cmdLabelNew(o *Opts) error {
	name := pyStrip(o.Pos[0])
	if name == "" || strings.ContainsAny(name, ",\t\r\n") {
		return die("a label name is one non-empty line without commas, got %q", o.Pos[0])
	}
	color := strings.TrimPrefix(o.str("color"), "#")
	if color != "" && !labelColorRe.MatchString(color) {
		return die("--color is six hex digits like 1d76db, got %q", o.str("color"))
	}
	short, err := a.msRepo(o)
	if err != nil {
		return err
	}
	if a.localOnly(short) {
		return die("%s is local-only: it has no GitHub labels to create", short)
	}
	if names, ok := a.repoLabels(short); ok {
		if c := exactLabel(name, names); c != "" {
			return die1("%s already has the label %q", short, c)
		}
	}
	if c := exactLabel(name, a.queuedLabels(short)); c != "" {
		return die1("%s already has a queued create for the label %q", short, c)
	}
	opID, _, err := a.enqueue("label_new", short+"#label:"+name, map[string]any{"name": name, "color": color, "description": o.str("description")})
	if err != nil {
		return err
	}
	a.printf("label %s %q  op %s\n", short, name, opID)
	return nil
}

// executeLabelNew creates the label; --force makes a repeat (or a label made meanwhile) update it, not fail.
func (a *App) executeLabelNew(op *Op) error {
	repo := a.repoFull(shortOf(op.Ref))
	if repo == "" {
		return &GhError{Msg: fmt.Sprintf("no GitHub repo behind alias %s", shortOf(op.Ref))}
	}
	return a.GH.CreateLabel(repo, LabelSpec{Name: argStr(op.Args, "name"), Color: argStr(op.Args, "color"), Description: argStr(op.Args, "description")})
}

// labelWaiting: a write that names a label whose create is still queued waits for it.
func labelWaiting(ops []*Op, i int) bool {
	o := ops[i]
	var used []string
	if k := kindOrNil(o.Op); k != nil && k.labelsUsed != nil {
		used = k.labelsUsed(o)
	}
	if len(used) == 0 {
		return false
	}
	for j, p := range ops {
		if j != i && p.Op == "label_new" && shortOf(p.Ref) == shortOf(o.Ref) && exactLabel(argStr(p.Args, "name"), used) != "" {
			return true
		}
	}
	return false
}
