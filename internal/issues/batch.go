package issues

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
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
	k := kindOrNil(o.Op)
	if k == nil || k.mutate == nil || (k.batchOK != nil && !k.batchOK(o)) {
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

// resolve fills c with the node ids the ops need: issues, labels to add or remove, a milestone by title.
func (a *App) resolve(short string, ops []*Op, c *idCache) error {
	repo := a.repoFull(short)
	var q IDQuery
	seenIssue := map[string]bool{}
	c.mu.Lock()
	for _, o := range ops {
		_, ident, _ := a.parseRef(o.Ref)
		if _, ok := c.issue[short+"#"+ident]; !ok && !seenIssue[ident] {
			seenIssue[ident] = true
			q.Issues = append(q.Issues, ident)
		}
		for _, l := range append(argList(o.Args, "add"), argList(o.Args, "remove")...) {
			k := short + "\x00" + l
			if _, ok := c.label[k]; !ok {
				q.Labels = append(q.Labels, l)
				c.label[k] = ""
			}
		}
		if o.Op == "milestone" && argStr(o.Args, "title") != "none" {
			if _, ok := c.mstone[short+"\x00"+argStr(o.Args, "title")]; !ok {
				q.Milestones = true
			}
		}
	}
	c.mu.Unlock()
	if len(q.Issues)+len(q.Labels) == 0 && !q.Milestones {
		return nil
	}
	ids, err := a.GH.ResolveIDs(repo, q)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for n, id := range ids.Issues {
		c.issue[short+"#"+n] = id
	}
	for l, id := range ids.Labels {
		c.label[short+"\x00"+l] = id
	}
	if ids.MilestonesAll {
		for t, id := range ids.Milestones {
			c.mstone[short+"\x00"+t] = id
		}
		for _, o := range ops {
			if t := argStr(o.Args, "title"); o.Op == "milestone" && t != "none" {
				if _, ok := c.mstone[short+"\x00"+t]; !ok {
					c.mstone[short+"\x00"+t] = ""
				}
			}
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
	k, err := kindOf(o.Op)
	if err != nil {
		return nil, err
	}
	if k.mutate == nil {
		return nil, &GhError{Msg: "graphql: " + o.Op + " is not batchable"}
	}
	return k.mutate(short, o, id, c)
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
	var flat []mutation
	var owner []int // flat[k] belongs to ops[owner[k]]
	for i, o := range ops {
		ms, err := a.mutationsFor(short, o, c)
		if err != nil {
			res[i].err = err
			continue
		}
		for _, m := range ms { // no mutations (a remove of labels the repo lacks) means nothing to send: done
			m.alias = "m" + strconv.Itoa(i) + m.alias
			flat = append(flat, m)
			owner = append(owner, i)
		}
	}
	if len(flat) == 0 {
		return res
	}
	failed, err := a.GH.Mutate(flat)
	if err != nil {
		return allFail(err)
	}
	msgs := make([][]string, len(ops))
	for k, f := range failed {
		if f != "" {
			msgs[owner[k]] = append(msgs[owner[k]], f)
		}
	}
	for i := range ops {
		if res[i].err == nil && len(msgs[i]) > 0 {
			res[i].err = &GhError{Msg: "graphql: " + trunc(strings.Join(msgs[i], "; "), 400)}
		}
	}
	return res
}
