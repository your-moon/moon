package issues

import (
	"fmt"
	"strings"
)

// An Action is something an agent can do to one issue. Each is defined once here: its name, the help line
// the dashboard's ":" verb list shows, which items it applies to (Allows), and how it runs (Session.Do).
// Completion, the dashboard's keys and its ":" line all read this table.
type Action struct {
	Name  string
	Help  string
	allow func(it Item, agent string) bool
	run   func(s *Session, ref, agent string, args []string) (string, error)
}

// Allows reports whether agent may run the action on it. It reads only the item, so it is safe for
// completion and for a list mark.
func (a Action) Allows(it Item, agent string) bool { return a.allow == nil || a.allow(it, agent) }

// HeldByOther returns the live claim on it when someone other than agent holds it, else nil. A stale claim,
// no claim and agent's own claim all return nil.
func HeldByOther(it Item, agent string) *ClaimInfo {
	if c := it.Claim; c != nil && !c.Stale && c.Agent != agent {
		return c
	}
	return nil
}

// ClaimedLive reports whether anyone holds a live (not stale) claim on it; the list's "@" mark.
func ClaimedLive(it Item) bool { return it.Claim != nil && !it.Claim.Stale }

var actions = []Action{
	{
		Name: "claim", Help: "claim an open issue (yours again renews the lease)",
		// open, and not held live by someone else: unclaimed, stale, or this agent's own (renew)
		allow: func(it Item, agent string) bool { return it.State == "open" && HeldByOther(it, agent) == nil },
		run: func(s *Session, ref, agent string, _ []string) (string, error) {
			msg, err := s.app.claim(ref, agent, 120*60, 0)
			return strings.TrimSpace(msg), err
		},
	},
	{
		Name: "release", Help: "drop your claim",
		allow: func(it Item, _ string) bool { return it.Claim != nil },
		run: func(s *Session, ref, agent string, _ []string) (string, error) {
			msg, err := s.app.release(ref, agent, false) // someone else's claim returns ErrHeld, never forced
			return strings.TrimSpace(msg), err
		},
	},
	{
		Name: "close", Help: "queue closing an open issue",
		allow: func(it Item, _ string) bool { return it.State == "open" },
		run: func(s *Session, ref, agent string, _ []string) (string, error) {
			_, err := s.app.closeIssue(ref, "completed", "", agent, false) // releases the agent's own claim
			return "queued close " + ref, err
		},
	},
	{
		Name: "reopen", Help: "queue reopening a closed issue",
		allow: func(it Item, _ string) bool { return it.State == "closed" },
		run: func(s *Session, ref, _ string, _ []string) (string, error) {
			_, err := s.app.reopen(ref)
			return "queued reopen " + ref, err
		},
	},
	{
		Name: "comment", Help: "queue a comment: :comment <ref> text",
		run: func(s *Session, ref, _ string, args []string) (string, error) {
			_, err := s.app.comment(ref, strings.Join(args, " "))
			return "queued comment on " + ref, err
		},
	},
	{
		Name: "label", Help: "queue label changes: :label <ref> add|remove <label>",
		run: func(s *Session, ref, _ string, args []string) (string, error) {
			if len(args) < 2 || (args[0] != "add" && args[0] != "remove") {
				return "", die("usage: label <ref> add|remove <label>")
			}
			var add, rm []string
			if args[0] == "add" {
				add = args[1:]
			} else {
				rm = args[1:]
			}
			_, err := s.app.labels(ref, add, rm)
			return fmt.Sprintf("queued label %s %s on %s", args[0], strings.Join(args[1:], ","), ref), err
		},
	},
	{
		Name: "milestone", Help: "queue a milestone: :milestone <ref> <title|none>",
		run: func(s *Session, ref, _ string, args []string) (string, error) {
			if len(args) == 0 {
				return "", die("usage: milestone <ref> <title|none>")
			}
			title := strings.Join(args, " ")
			_, err := s.app.setMilestone(ref, title)
			return "queued milestone " + title + " on " + ref, err
		},
	},
}

// Actions lists every item action in the order the dashboard's verb list shows them.
func Actions() []Action { return append([]Action(nil), actions...) }

// ActionNamed finds an action by name.
func ActionNamed(name string) (Action, bool) {
	for _, a := range actions {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

// Do runs the named action on ref as agent and returns the footer message. The write is queued locally (or a
// claim file written) like the CLI command of the same name, and queued writes start the background push
// themselves. Do does not check Allows: callers that can see the item do (and word the refusal); a claim
// or release held by someone else is refused here with ErrHeld.
func (s *Session) Do(name, ref, agent string, args []string) (string, error) {
	a, ok := ActionNamed(name)
	if !ok {
		return "", fmt.Errorf("unknown action %q", name)
	}
	return a.run(s, ref, agent, args)
}
