package issues

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RemoteMilestone is a milestone as GitHub reports it. DueOn is the date part (YYYY-MM-DD) or empty.
type RemoteMilestone struct {
	Number      int
	Title       string
	State       string // open or closed
	DueOn       string
	Description string
}

// MilestoneChange is a partial update: nil fields are left alone. ClearDue removes the due date.
type MilestoneChange struct {
	Title, Description, State, Due *string
	ClearDue                       bool
}

type ghRun func(args []string, stdin string, timeout time.Duration) (string, error)

// dueOnJSON is the due_on GitHub is sent for a date. Not midnight: a create with T00:00:00Z comes back a day
// early (GitHub reads it in a local zone), so the date is sent at noon UTC, which keeps it on every continent.
func dueOnJSON(date string) any {
	if date == "" {
		return nil
	}
	return date + "T12:00:00Z"
}

type remoteMsJSON struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	State       string  `json:"state"`
	DueOn       *string `json:"due_on"`
	Description *string `json:"description"`
}

func (j remoteMsJSON) rm() RemoteMilestone {
	r := RemoteMilestone{Number: j.Number, Title: j.Title, State: strings.ToLower(j.State)}
	if j.DueOn != nil {
		r.DueOn = dueDate(*j.DueOn)
	}
	if j.Description != nil {
		r.Description = *j.Description
	}
	return r
}

// ghListMilestones, ghCreateMilestone and ghUpdateMilestone are the three `gh api` calls behind the
// GitHub interface. They take the Run function so every implementation of the interface shares them.
func ghListMilestones(run ghRun, repo string) ([]RemoteMilestone, error) {
	out, err := run([]string{"api", fmt.Sprintf("repos/%s/milestones?state=all&per_page=100", repo), "--paginate",
		"--jq", ".[] | {number: .number, title: .title, state: .state, due_on: .due_on, description: .description}"}, "", 90*time.Second)
	if err != nil {
		return nil, err
	}
	var res []RemoteMilestone
	for _, line := range strings.Split(out, "\n") {
		if pyStrip(line) == "" {
			continue
		}
		var j remoteMsJSON
		if err := json.Unmarshal([]byte(line), &j); err != nil {
			return nil, &GhError{Msg: "bad milestone list from gh: " + trunc(line, 200)}
		}
		res = append(res, j.rm())
	}
	return res, nil
}

func ghCreateMilestone(run ghRun, repo string, m RemoteMilestone) (RemoteMilestone, error) {
	body := map[string]any{"title": m.Title, "state": "open", "description": m.Description}
	if m.DueOn != "" {
		body["due_on"] = dueOnJSON(m.DueOn)
	}
	b, _ := json.Marshal(body)
	out, err := run([]string{"api", "-X", "POST", fmt.Sprintf("repos/%s/milestones", repo), "--input", "-"}, string(b), 90*time.Second)
	if err != nil {
		return RemoteMilestone{}, err
	}
	var j remoteMsJSON
	if err := json.Unmarshal([]byte(out), &j); err != nil || j.Number == 0 {
		return RemoteMilestone{}, &GhError{Msg: "create milestone printed no number: " + trunc(out, 200)}
	}
	return j.rm(), nil
}

func ghUpdateMilestone(run ghRun, repo string, number int, ch MilestoneChange) error {
	body := map[string]any{}
	if ch.Title != nil {
		body["title"] = *ch.Title
	}
	if ch.Description != nil {
		body["description"] = *ch.Description
	}
	if ch.State != nil {
		body["state"] = *ch.State
	}
	if ch.ClearDue {
		body["due_on"] = nil
	} else if ch.Due != nil {
		body["due_on"] = dueOnJSON(*ch.Due)
	}
	b, _ := json.Marshal(body)
	_, err := run([]string{"api", "-X", "PATCH", fmt.Sprintf("repos/%s/milestones/%d", repo, number), "--input", "-"}, string(b), 90*time.Second)
	return err
}

func (g ExecGH) ListMilestones(repo string) ([]RemoteMilestone, error) {
	return ghListMilestones(g.Run, repo)
}
func (g ExecGH) CreateMilestone(repo string, m RemoteMilestone) (RemoteMilestone, error) {
	return ghCreateMilestone(g.Run, repo, m)
}
func (g ExecGH) UpdateMilestone(repo string, number int, ch MilestoneChange) error {
	return ghUpdateMilestone(g.Run, repo, number, ch)
}
