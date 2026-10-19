package issues

import (
	"sort"
	"strings"
)

// editFields are the fields this command line asks to change. --body-file counts as body.
func editFields(o *Opts) []string {
	var out []string
	for _, f := range []string{"title", "body", "label", "milestone", "add", "remove"} {
		if o.Seen[f] {
			out = append(out, f)
		}
	}
	if o.Seen["body-file"] && !o.Seen["body"] {
		out = append(out, "body")
	}
	sort.Strings(out)
	return out
}

func (a *App) cmdOutboxEdit(o *Opts) error {
	op, err := a.findOp(o.Pos[0])
	if err != nil {
		return err
	}
	fields := editFields(o)
	if len(fields) == 0 {
		return die("outbox edit needs a field to change: --title, --body/--body-file, --label, --milestone, --add or --remove")
	}
	k, err := kindOf(op.Op)
	if err != nil {
		return err
	}
	allowed := k.editable
	for _, f := range fields {
		if !contains(allowed, f) {
			return die("%s is a %s write: it can change %s, not --%s", shortOpID(op.OpID), op.Op, orDash(strings.Join(allowed, ", ")), f)
		}
	}
	return a.withQueueLock(func() error {
		args, err := a.editedArgs(op, o)
		if err != nil {
			return err
		}
		op.Args, op.LastError, op.Permanent = args, nil, false // the old error was about the old fields
		if err := a.mirror().saveOp(op); err != nil {
			return err
		}
		a.printf("edited %s  %s %s  %s\n", op.OpID, op.Op, op.Ref, oneLine(outboxSummary(op)))
		return nil
	})
}

// editedArgs is a copy of the op's args with the requested fields replaced and checked like the command that
// queued the op would have checked them.
func (a *App) editedArgs(op *Op, o *Opts) (map[string]any, error) {
	args := map[string]any{}
	for k, v := range op.Args {
		args[k] = v
	}
	short := shortOf(op.Ref)
	if o.Seen["title"] {
		if pyStrip(o.str("title")) == "" {
			return nil, die("the title must not be empty")
		}
		args["title"] = o.str("title")
	}
	if err := a.editBody(op, o, args); err != nil {
		return nil, err
	}
	if o.Seen["label"] {
		l, err := a.checkLabels(short, o.L["label"])
		if err != nil {
			return nil, err
		}
		args["labels"] = toAny(l)
	}
	for _, k := range []string{"add", "remove"} {
		if !o.Seen[k] {
			continue
		}
		l := o.L[k]
		if k == "add" {
			var err error
			if l, err = a.checkLabels(short, l); err != nil {
				return nil, err
			}
		}
		args[k] = toAny(l)
	}
	if o.Seen["milestone"] {
		t, err := a.editMilestone(short, o.str("milestone"))
		if err != nil {
			return nil, err
		}
		switch {
		case op.Op == "milestone":
			args["title"] = t
		case t == "" || t == "none": // a queued create with no milestone carries nil, as cmdNew writes it
			args["milestone"] = nil
		default:
			args["milestone"] = t
		}
	}
	return args, nil
}

// editMilestone turns M-<n> into the milestone's title. Other text is the title as typed.
func (a *App) editMilestone(short, typed string) (string, error) {
	if !msRefRe.MatchString(typed) {
		return typed, nil
	}
	t, _, err := a.msTitleOfRef(short, typed)
	return t, err
}

// editBody sets args["body"] from --body/--body-file. A queued `new` keeps its local-id marker, which is what
// makes a retried create idempotent. A blank body is refused as everywhere else; a comment cannot be blank.
func (a *App) editBody(op *Op, o *Opts, args map[string]any) error {
	if !o.Seen["body"] && !o.Seen["body-file"] {
		return nil
	}
	body, _, err := a.readBodyGuarded(o, "outbox edit", false)
	if err != nil {
		return err
	}
	k, err := kindOf(op.Op)
	if err != nil {
		return err
	}
	if k.bodyEdit != nil {
		if body, err = k.bodyEdit(op, body); err != nil {
			return err
		}
	}
	args["body"] = body
	return nil
}
