package issues

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Batched push (moon#18): independent queued writes on existing issues go out as aliased GraphQL mutations,
// up to pushBatch ops per request, instead of one `gh` round trip each.
//
// What stays on the single-op path (execute): a `new` (its create is idempotent through the local-id marker),
// milestone and label definition writes, a comment retried after an uncertain attempt (it first looks for its
// op-id marker), and anything on a local ref. nextBatch still hands out one op per order key, so a batch never
// holds two writes of one issue; the next one goes in a later request, after this one's result is in.

// pushBatch is the most ops in one request; 1 turns batching off. Tests change it.
var pushBatch = 20

// minBatch: with fewer ready writes in a repo than this, REST is as cheap as the resolve-then-mutate pair.
const minBatch = 3

func (a *App) batchable(o *Op) bool {
	switch o.Op {
	case "comment":
		if o.Inflight {
			return false
		}
	case "close", "reopen", "label", "milestone", "edit":
	default:
		return false
	}
	_, ident, err := a.parseRef(o.Ref)
	return err == nil && isDigits(ident)
}

// idCache keeps node ids for one push run, so later rounds skip the resolve request.
type idCache struct {
	mu     sync.Mutex
	issue  map[string]string // short#N
	label  map[string]string // short\x00name; "" = looked for, not found
	mstone map[string]string // short\x00title; "" = looked for, not found
}

func newIDCache() *idCache {
	return &idCache{issue: map[string]string{}, label: map[string]string{}, mstone: map[string]string{}}
}

type gqlErr struct {
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// gqlRaw runs a GraphQL request and returns data and errors separately: a partly failed request still has
// data for the parts that worked. A failure with no JSON body comes back as err.
func (a *App) gqlRaw(query string, vars map[string]any) (map[string]json.RawMessage, []gqlErr, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	s, err := a.GH.Run([]string{"api", "graphql", "--input", "-"}, string(body), 120*time.Second)
	var d struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []gqlErr                   `json:"errors"`
	}
	if jerr := json.Unmarshal([]byte(s), &d); jerr != nil || (d.Data == nil && len(d.Errors) == 0) {
		if err == nil {
			err = &GhError{Msg: "graphql: unreadable response: " + trunc(s, 200)}
		}
		return nil, nil, err
	}
	return d.Data, d.Errors, nil
}

func errsText(es []gqlErr) string {
	var m []string
	for _, e := range es {
		m = append(m, e.Message)
	}
	return trunc(strings.Join(m, "; "), 400)
}

// resolve fills c with the node ids the ops need: issues, labels to add or remove, a milestone by title.
func (a *App) resolve(short string, ops []*Op, c *idCache) error {
	owner, name, _ := strings.Cut(a.repoFull(short), "/")
	vars := map[string]any{"owner": owner, "name": name}
	var sel []string
	decl := []string{"$owner:String!", "$name:String!"}
	needMs := false
	labelVar := map[string]string{} // alias -> label name
	seenIssue := map[string]bool{}
	c.mu.Lock()
	for _, o := range ops {
		_, ident, _ := a.parseRef(o.Ref)
		if _, ok := c.issue[short+"#"+ident]; !ok && !seenIssue[ident] {
			seenIssue[ident] = true
			sel = append(sel, fmt.Sprintf("i%s:issue(number:%s){id}", ident, ident))
		}
		for _, l := range append(argList(o.Args, "add"), argList(o.Args, "remove")...) {
			k := short + "\x00" + l
			if _, ok := c.label[k]; !ok {
				v := fmt.Sprintf("l%d", len(labelVar))
				labelVar[v] = l
				vars[v] = l
				decl = append(decl, "$"+v+":String!")
				sel = append(sel, fmt.Sprintf("%s:label(name:$%s){id}", v, v))
				c.label[k] = ""
			}
		}
		if o.Op == "milestone" && argStr(o.Args, "title") != "none" {
			if _, ok := c.mstone[short+"\x00"+argStr(o.Args, "title")]; !ok {
				needMs = true
			}
		}
	}
	c.mu.Unlock()
	if needMs {
		sel = append(sel, "milestones(first:100,states:[OPEN,CLOSED]){pageInfo{hasNextPage} nodes{id title}}")
	}
	if len(sel) == 0 {
		return nil
	}
	sort.Strings(sel)
	q := "query(" + strings.Join(decl, ",") + "){repository(owner:$owner,name:$name){" + strings.Join(sel, " ") + "}}"
	data, errs, err := a.gqlRaw(q, vars)
	if err != nil {
		return err
	}
	var repo map[string]json.RawMessage
	json.Unmarshal(data["repository"], &repo)
	if repo == nil {
		return &GhError{Msg: "graphql: " + errsText(errs)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, raw := range repo {
		var n struct {
			ID    string `json:"id"`
			Nodes []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"nodes"`
			PageInfo pageInfo `json:"pageInfo"`
		}
		if json.Unmarshal(raw, &n) != nil {
			continue
		}
		switch {
		case k == "milestones":
			if n.PageInfo.HasNextPage { // more than one page of milestones: unknown titles stay unknown
				continue
			}
			for _, m := range n.Nodes {
				c.mstone[short+"\x00"+m.Title] = m.ID
			}
			for _, o := range ops {
				if t := argStr(o.Args, "title"); o.Op == "milestone" && t != "none" {
					if _, ok := c.mstone[short+"\x00"+t]; !ok {
						c.mstone[short+"\x00"+t] = ""
					}
				}
			}
		case labelVar[k] != "":
			c.label[short+"\x00"+labelVar[k]] = n.ID
		case strings.HasPrefix(k, "i") && n.ID != "":
			c.issue[short+"#"+k[1:]] = n.ID
		}
	}
	return nil
}

type mutation struct {
	alias, field, inType string
	input                map[string]any
}

// mutationsFor turns one op into its mutations, or says why it cannot be sent (unknown issue, label, milestone).
func (a *App) mutationsFor(short string, o *Op, c *idCache) ([]mutation, error) {
	_, ident, _ := a.parseRef(o.Ref)
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.issue[short+"#"+ident]
	if id == "" {
		return nil, &GhError{Msg: fmt.Sprintf("graphql: no issue %s#%s on GitHub", short, ident)}
	}
	args := o.Args
	switch o.Op {
	case "comment":
		return []mutation{{"c", "addComment", "AddCommentInput", map[string]any{"subjectId": id,
			"body": pyRstrip(argStr(args, "body")) + "\n\n<!-- op-id: " + o.OpID + " -->"}}}, nil
	case "close":
		reason := "COMPLETED"
		if argStr(args, "reason") == "not_planned" {
			reason = "NOT_PLANNED"
		}
		var ms []mutation
		if cm := argStr(args, "comment"); cm != "" {
			ms = append(ms, mutation{"c", "addComment", "AddCommentInput", map[string]any{"subjectId": id, "body": cm}})
		}
		return append(ms, mutation{"x", "closeIssue", "CloseIssueInput", map[string]any{"issueId": id, "stateReason": reason}}), nil
	case "reopen":
		return []mutation{{"x", "reopenIssue", "ReopenIssueInput", map[string]any{"issueId": id}}}, nil
	case "label":
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
	case "milestone":
		in := map[string]any{"id": id, "milestoneId": nil}
		if t := argStr(args, "title"); t != "none" {
			mid := c.mstone[short+"\x00"+t]
			if mid == "" {
				return nil, &GhError{Msg: fmt.Sprintf("graphql: milestone %q not found in %s", t, short)}
			}
			in["milestoneId"] = mid
		}
		return []mutation{{"m", "updateIssue", "UpdateIssueInput", in}}, nil
	case "edit":
		in := map[string]any{"id": id}
		if t, ok := args["title"]; ok {
			in["title"] = t
		}
		if b, ok := args["body"]; ok {
			in["body"] = b
		}
		return []mutation{{"e", "updateIssue", "UpdateIssueInput", in}}, nil
	}
	return nil, &GhError{Msg: "unknown op " + o.Op}
}

// sendBatch sends ops (one repo, one per order key) and returns one result per op, in order.
func (a *App) sendBatch(short string, ops []*Op, c *idCache) []sent {
	res := make([]sent, len(ops))
	allFail := func(err error) []sent {
		for i := range res {
			if res[i].err == nil {
				res[i].err = err
			}
		}
		return res
	}
	if err := a.resolve(short, ops, c); err != nil {
		return allFail(err)
	}
	var decl, body []string
	vars := map[string]any{}
	opAliases := make([][]string, len(ops))
	for i, o := range ops {
		ms, err := a.mutationsFor(short, o, c)
		if err != nil {
			res[i].err = err
			continue
		}
		for _, m := range ms { // no mutations (a remove of labels the repo lacks) means nothing to send: done
			al := "m" + strconv.Itoa(i) + m.alias
			decl = append(decl, fmt.Sprintf("$%s:%s!", al, m.inType))
			body = append(body, fmt.Sprintf("%s:%s(input:$%s){clientMutationId}", al, m.field, al))
			vars[al] = m.input
			opAliases[i] = append(opAliases[i], al)
		}
	}
	if len(body) == 0 {
		return res
	}
	data, errs, err := a.gqlRaw("mutation("+strings.Join(decl, ",")+"){"+strings.Join(body, " ")+"}", vars)
	if err != nil {
		return allFail(err)
	}
	failed := map[string][]string{} // alias -> messages
	var general []gqlErr            // errors that name no alias
	for _, e := range errs {
		if al, ok := firstString(e.Path); ok {
			failed[al] = append(failed[al], e.Message)
		} else {
			general = append(general, e)
		}
	}
	for i := range ops {
		if res[i].err != nil {
			continue
		}
		var msgs []string
		for _, al := range opAliases[i] {
			msgs = append(msgs, failed[al]...)
			if raw, ok := data[al]; (!ok || string(raw) == "null") && len(failed[al]) == 0 {
				msgs = append(msgs, "no result for "+al+" "+errsText(general))
			}
		}
		if len(msgs) > 0 {
			res[i].err = &GhError{Msg: "graphql: " + trunc(strings.Join(msgs, "; "), 400)}
		}
	}
	return res
}

func firstString(p []any) (string, bool) {
	if len(p) == 0 {
		return "", false
	}
	s, ok := p[0].(string)
	return s, ok
}
