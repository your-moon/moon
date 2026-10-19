package issues

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The Outbox op kinds. Each kind of queued write is defined once, in opKinds below, with everything that
// differs per kind (describe, applyEff, applyLocal and send are required; a kind without an effect says so
// with noEff or notLocal): how it shows in the effective view, in a local-only issue file or a milestone record,
// how it is described, sent (REST) and batched (GraphQL), what `outbox edit` may change, and what it makes
// other writes wait for. A switch over Op.Op elsewhere is a bug: ask kindOf. An unknown kind is an error.

type opFamily int

const (
	famIssue opFamily = iota
	famMilestone
	famLabel
)

// sendCtx is what the REST send of an issue write needs: the parsed ref and the repo.
type sendCtx struct{ short, ident, repo string }

// localEdit is a local-only issue file being changed by one write.
type localEdit struct {
	node *issueNode
	add  func(body string) // appends a comment
}

type opKind struct {
	name   string
	family opFamily
	// creates: the write makes the issue, so it may run on a local ref (every other issue write must wait).
	creates bool

	describe   func(o *Op) string
	applyEff   func(e *eff, o *Op)                           // the issue's effective state
	applyLocal func(l *localEdit, args map[string]any) error // a local-only issue file
	applyMs    func(r *msRec, args map[string]any)           // a milestone record (milestone family)
	send       func(a *App, o *Op, c sendCtx) (int, error)   // REST via gh; returns the new issue number for a create
	// mutate is the GraphQL form for a batched push; nil = not batchable. batchOK may veto one op.
	mutate  func(short string, o *Op, id string, c *idCache) ([]mutation, error)
	batchOK func(o *Op) bool

	editable []string // the fields `outbox edit` may change; none = not editable
	// bodyEdit turns the new body given to `outbox edit` into the one stored in args.
	bodyEdit func(op *Op, body string) (string, error)
	// waitTitle is the milestone title the write names; labelsUsed the labels. Such a write waits for a
	// queued create of them.
	waitTitle  func(o *Op) string
	labelsUsed func(o *Op) []string
}

var opKinds map[string]*opKind

// kindOrNil is the kind, or nil when the name is not one we know.
func kindOrNil(name string) *opKind { return opKinds[name] }

// kindOf is the kind, or an error for a name we do not know.
func kindOf(name string) (*opKind, error) {
	if k := opKinds[name]; k != nil {
		return k, nil
	}
	return nil, &GhError{Msg: "unknown op " + name}
}

// applyOps applies queued writes to the effective view of an issue, oldest first. A kind that is not in the
// table is an error.
func applyOps(e *eff, ops []*Op) error {
	for _, o := range ops {
		k, err := kindOf(o.Op)
		if err != nil {
			return err
		}
		k.applyEff(e, o)
	}
	return nil
}

// applyPending is applyOps for the places that only show state and cannot fail: a queued write of a kind we
// do not know is named on stderr and left out, never dropped without a word.
func applyPending(e *eff, ops []*Op) {
	if err := applyOps(e, ops); err != nil {
		fmt.Fprintln(os.Stderr, "moon: queued write skipped:", err)
	}
}

// describe is the one-line text of a queued write.
func describe(o *Op) string {
	if k := kindOrNil(o.Op); k != nil {
		return k.describe(o)
	}
	return o.Op
}

func isMsOp(op string) bool { k := kindOrNil(op); return k != nil && k.family == famMilestone }

func noEff(*eff, *Op)                {}
func noMsRec(*msRec, map[string]any) {}
func nameOnly(o *Op) string          { return o.Op }
func notLocal(*localEdit, map[string]any) error {
	return fmt.Errorf("this write has no local-only form")
}

func applyLabels(e *eff, args map[string]any) {
	rm, add := argList(args, "remove"), argList(args, "add")
	var nl []string
	for _, l := range e.labels {
		if !contains(rm, l) {
			nl = append(nl, l)
		}
	}
	for _, l := range add {
		if !contains(e.labels, l) {
			nl = append(nl, l)
		}
	}
	e.labels = nl
}

func init() {
	opKinds = map[string]*opKind{}
	add := func(k *opKind) {
		if k.applyMs == nil {
			k.applyMs = noMsRec
		}
		opKinds[k.name] = k
	}

	add(&opKind{name: "new", creates: true, describe: nameOnly, applyEff: noEff, applyLocal: notLocal,
		send:     sendNew,
		editable: []string{"title", "body", "label", "milestone"},
		bodyEdit: func(op *Op, body string) (string, error) {
			return pyRstrip(body) + "\n\n<!-- local-id: " + argStr(op.Args, "local_id") + " -->", nil
		},
		waitTitle:  func(o *Op) string { return argStr(o.Args, "milestone") },
		labelsUsed: func(o *Op) []string { return argList(o.Args, "labels") }})

	add(&opKind{name: "comment", applyEff: noEff,
		describe: func(o *Op) string {
			return "comment [" + o.OpID + "]: " + trunc(oneLine(argStr(o.Args, "body")), 300)
		},
		applyLocal: func(l *localEdit, args map[string]any) error { l.add(argStr(args, "body")); return nil },
		send:       sendComment,
		mutate: func(short string, o *Op, id string, c *idCache) ([]mutation, error) {
			return []mutation{{"c", "addComment", "AddCommentInput", map[string]any{"subjectId": id,
				"body": pyRstrip(argStr(o.Args, "body")) + "\n\n<!-- op-id: " + o.OpID + " -->"}}}, nil
		},
		batchOK:  func(o *Op) bool { return !o.Inflight },
		editable: []string{"body"},
		bodyEdit: func(op *Op, body string) (string, error) {
			if pyStrip(body) == "" {
				return "", die("a comment needs a body; drop it with: moon issues outbox drop %s", shortOpID(op.OpID))
			}
			return body, nil
		}})

	add(&opKind{name: "close",
		describe: func(o *Op) string {
			reason := argStr(o.Args, "reason")
			if reason == "" {
				reason = "completed"
			}
			s := "close (" + reason + ")"
			if c := argStr(o.Args, "comment"); c != "" {
				s += " + comment: " + c
			}
			return s
		},
		applyEff: func(e *eff, o *Op) { e.state = "closed" },
		applyLocal: func(l *localEdit, args map[string]any) error {
			l.node.State = "CLOSED"
			r := "COMPLETED"
			if argStr(args, "reason") == "not_planned" {
				r = "NOT_PLANNED"
			}
			l.node.StateReason = &r
			if c := argStr(args, "comment"); c != "" {
				l.add(c)
			}
			return nil
		},
		send: func(a *App, o *Op, c sendCtx) (int, error) {
			reason := "completed"
			if argStr(o.Args, "reason") == "not_planned" {
				reason = "not planned"
			}
			ga := []string{"issue", "close", c.ident, "-R", c.repo, "--reason", reason}
			if cm := argStr(o.Args, "comment"); cm != "" {
				ga = append(ga, "--comment", cm)
			}
			_, err := a.gh(ga, "")
			return 0, err
		},
		mutate: func(short string, o *Op, id string, c *idCache) ([]mutation, error) {
			reason := "COMPLETED"
			if argStr(o.Args, "reason") == "not_planned" {
				reason = "NOT_PLANNED"
			}
			var ms []mutation
			if cm := argStr(o.Args, "comment"); cm != "" {
				ms = append(ms, mutation{"c", "addComment", "AddCommentInput", map[string]any{"subjectId": id, "body": cm}})
			}
			return append(ms, mutation{"x", "closeIssue", "CloseIssueInput", map[string]any{"issueId": id, "stateReason": reason}}), nil
		}})

	add(&opKind{name: "reopen", describe: nameOnly,
		applyEff: func(e *eff, o *Op) { e.state = "open" },
		applyLocal: func(l *localEdit, args map[string]any) error {
			l.node.State, l.node.StateReason = "OPEN", nil
			return nil
		},
		send: func(a *App, o *Op, c sendCtx) (int, error) {
			_, err := a.gh([]string{"issue", "reopen", c.ident, "-R", c.repo}, "")
			return 0, err
		},
		mutate: func(short string, o *Op, id string, c *idCache) ([]mutation, error) {
			return []mutation{{"x", "reopenIssue", "ReopenIssueInput", map[string]any{"issueId": id}}}, nil
		}})

	add(&opKind{name: "label",
		describe: func(o *Op) string {
			return "label +" + strings.Join(argList(o.Args, "add"), ",") + " -" + strings.Join(argList(o.Args, "remove"), ",")
		},
		applyEff: func(e *eff, o *Op) { applyLabels(e, o.Args) },
		applyLocal: func(l *localEdit, args map[string]any) error {
			e := &eff{}
			for _, x := range l.node.Labels.Nodes {
				e.labels = append(e.labels, x.Name)
			}
			applyLabels(e, args)
			l.node.Labels.Nodes = nil
			for _, x := range e.labels {
				l.node.Labels.Nodes = append(l.node.Labels.Nodes, struct {
					Name string `json:"name"`
				}{x})
			}
			return nil
		},
		send: func(a *App, o *Op, c sendCtx) (int, error) {
			ga := []string{"issue", "edit", c.ident, "-R", c.repo}
			if l := argList(o.Args, "add"); len(l) > 0 {
				ga = append(ga, "--add-label", strings.Join(l, ","))
			}
			if l := argList(o.Args, "remove"); len(l) > 0 {
				ga = append(ga, "--remove-label", strings.Join(l, ","))
			}
			_, err := a.gh(ga, "")
			return 0, err
		},
		mutate:     mutateLabel,
		editable:   []string{"add", "remove"},
		labelsUsed: func(o *Op) []string { return argList(o.Args, "add") }})

	add(&opKind{name: "milestone",
		describe: func(o *Op) string { return "milestone -> " + argStr(o.Args, "title") },
		applyEff: func(e *eff, o *Op) {
			if t := argStr(o.Args, "title"); t == "none" {
				e.milestone = ""
			} else {
				e.milestone = t
			}
		},
		applyLocal: func(l *localEdit, args map[string]any) error {
			if t := argStr(args, "title"); t == "none" || t == "" {
				l.node.Milestone = nil
			} else {
				l.node.Milestone = &struct {
					Title string `json:"title"`
				}{t}
			}
			return nil
		},
		send: func(a *App, o *Op, c sendCtx) (int, error) {
			ga := []string{"issue", "edit", c.ident, "-R", c.repo}
			if argStr(o.Args, "title") == "none" {
				ga = append(ga, "--remove-milestone")
			} else {
				ga = append(ga, "--milestone", argStr(o.Args, "title"))
			}
			_, err := a.gh(ga, "")
			return 0, err
		},
		mutate: func(short string, o *Op, id string, c *idCache) ([]mutation, error) {
			in := map[string]any{"id": id, "milestoneId": nil}
			if t := argStr(o.Args, "title"); t != "none" {
				mid := c.mstone[short+"\x00"+t]
				if mid == "" {
					return nil, &GhError{Msg: fmt.Sprintf("graphql: milestone %q not found in %s", t, short)}
				}
				in["milestoneId"] = mid
			}
			return []mutation{{"m", "updateIssue", "UpdateIssueInput", in}}, nil
		},
		editable: []string{"milestone"},
		waitTitle: func(o *Op) string {
			if t := argStr(o.Args, "title"); t != "none" {
				return t
			}
			return ""
		}})

	add(&opKind{name: "edit",
		describe: func(o *Op) string {
			var p []string
			for _, x := range []string{"title", "body"} {
				if _, ok := o.Args[x]; ok {
					p = append(p, x)
				}
			}
			return "edit " + strings.Join(p, ", ")
		},
		applyEff: func(e *eff, o *Op) {
			if t, ok := o.Args["title"].(string); ok {
				e.title = t
			}
			if t, ok := o.Args["body"].(string); ok {
				e.body = t
			}
		},
		applyLocal: func(l *localEdit, args map[string]any) error {
			if t, ok := args["title"].(string); ok {
				l.node.Title = t
			}
			if b, ok := args["body"].(string); ok {
				l.node.Body = b
			}
			return nil
		},
		send: func(a *App, o *Op, c sendCtx) (int, error) {
			ga := []string{"issue", "edit", c.ident, "-R", c.repo}
			if t, ok := o.Args["title"]; ok {
				ts, _ := t.(string)
				ga = append(ga, "--title", ts)
			}
			body, hasBody := o.Args["body"]
			if hasBody {
				ga = append(ga, "--body-file", "-")
			}
			bs, _ := body.(string)
			_, err := a.gh(ga, bs)
			return 0, err
		},
		mutate: func(short string, o *Op, id string, c *idCache) ([]mutation, error) {
			in := map[string]any{"id": id}
			if t, ok := o.Args["title"]; ok {
				in["title"] = t
			}
			if b, ok := o.Args["body"]; ok {
				in["body"] = b
			}
			return []mutation{{"e", "updateIssue", "UpdateIssueInput", in}}, nil
		},
		editable: []string{"title", "body"}})

	add(&opKind{name: "label_new", family: famLabel, applyEff: noEff, applyLocal: notLocal,
		describe: func(o *Op) string { return "create label " + argStr(o.Args, "name") },
		send:     func(a *App, o *Op, _ sendCtx) (int, error) { return 0, a.executeLabelNew(o) }})

	add(&opKind{name: "ms_new", family: famMilestone, describe: nameOnly, applyEff: noEff, applyLocal: notLocal,
		send: func(a *App, o *Op, _ sendCtx) (int, error) { return 0, a.executeMsNew(o) }})
	msChange := func(name string, apply func(r *msRec, args map[string]any), change func(ch *MilestoneChange, args map[string]any)) {
		add(&opKind{name: name, family: famMilestone, applyMs: apply, describe: nameOnly, applyEff: noEff, applyLocal: notLocal,
			send:      func(a *App, o *Op, _ sendCtx) (int, error) { return 0, a.executeMsChange(o, change) },
			waitTitle: func(o *Op) string { return argStr(o.Args, "title") }})
	}
	msChange("ms_close", func(r *msRec, _ map[string]any) { r.State = "closed" },
		func(ch *MilestoneChange, _ map[string]any) { s := "closed"; ch.State = &s })
	msChange("ms_reopen", func(r *msRec, _ map[string]any) { r.State = "open" },
		func(ch *MilestoneChange, _ map[string]any) { s := "open"; ch.State = &s })
	msChange("ms_edit", func(r *msRec, args map[string]any) {
		if t, ok := args["new_title"].(string); ok {
			r.Title = t
		}
		if d, ok := args["due"].(string); ok {
			if d == "none" {
				d = ""
			}
			r.Due = d
		}
		if d, ok := args["description"].(string); ok {
			r.Desc = d
		}
	}, func(ch *MilestoneChange, args map[string]any) {
		if t, ok := args["new_title"].(string); ok {
			ch.Title = &t
		}
		if d, ok := args["due"].(string); ok {
			if d == "none" {
				ch.ClearDue = true
			} else {
				ch.Due = &d
			}
		}
		if d, ok := args["description"].(string); ok {
			ch.Description = &d
		}
	})
}

// sendNew creates the issue, or finds the one a crashed earlier attempt made (the local-id marker).
func sendNew(a *App, op *Op, c sendCtx) (int, error) {
	args := op.Args
	n, err := a.findByMarker(c.short, argStr(args, "local_id"), op.TS)
	if err != nil {
		return 0, err
	}
	if n != 0 {
		return n, nil
	}
	ga := []string{"issue", "create", "-R", c.repo, "--title", argStr(args, "title"), "--body-file", "-"}
	for _, l := range argList(args, "labels") {
		ga = append(ga, "--label", l)
	}
	if m := argStr(args, "milestone"); m != "" {
		ga = append(ga, "--milestone", m)
	}
	out, err := a.gh(ga, argStr(args, "body"))
	if err != nil {
		return 0, err
	}
	m := issueURLRe.FindStringSubmatch(out)
	if m == nil {
		return 0, &GhError{Msg: fmt.Sprintf("create printed no issue url: %q", trunc(out, 200))}
	}
	n, _ = strconv.Atoi(m[1])
	return n, nil
}

func sendComment(a *App, op *Op, c sendCtx) (int, error) {
	marker := "<!-- op-id: " + op.OpID + " -->"
	if op.Inflight { // a previous attempt may have posted it before we could record that
		bodies, err := a.gh([]string{"api", fmt.Sprintf("repos/%s/issues/%s/comments", c.repo, c.ident), "--paginate", "--jq", ".[].body"}, "")
		if err != nil {
			return 0, err
		}
		if strings.Contains(bodies, marker) {
			return 0, nil
		}
	}
	_, err := a.gh([]string{"issue", "comment", c.ident, "-R", c.repo, "--body-file", "-"}, pyRstrip(argStr(op.Args, "body"))+"\n\n"+marker)
	return 0, err
}

func mutateLabel(short string, o *Op, id string, c *idCache) ([]mutation, error) {
	args := o.Args
	var ms []mutation
	var add, rm []string
	for _, l := range argList(args, "add") {
		lid := c.label[short+"\x00"+l]
		if lid == "" {
			return nil, &GhError{Msg: fmt.Sprintf("graphql: label %q not found in %s", l, short)}
		}
		add = append(add, lid)
	}
	for _, l := range argList(args, "remove") {
		if lid := c.label[short+"\x00"+l]; lid != "" { // not in the repo: it cannot be on the issue
			rm = append(rm, lid)
		}
	}
	if len(add) > 0 {
		ms = append(ms, mutation{"a", "addLabelsToLabelable", "AddLabelsToLabelableInput", map[string]any{"labelableId": id, "labelIds": add}})
	}
	if len(rm) > 0 {
		ms = append(ms, mutation{"r", "removeLabelsFromLabelable", "RemoveLabelsFromLabelableInput", map[string]any{"labelableId": id, "labelIds": rm}})
	}
	return ms, nil
}
