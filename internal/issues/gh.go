package issues

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GitHub is the only door to GitHub. Production shells out to `gh`; tests use a fake.
type GitHub interface {
	// Run executes `gh <args>` with stdin and returns stdout.
	Run(args []string, stdin string, timeout time.Duration) (string, error)
	// ListMilestones, CreateMilestone and UpdateMilestone wrap `gh api repos/{owner}/{repo}/milestones`.
	ListMilestones(repo string) ([]RemoteMilestone, error)
	CreateMilestone(repo string, m RemoteMilestone) (RemoteMilestone, error)
	UpdateMilestone(repo string, number int, ch MilestoneChange) error
}

// GhError is a failed gh call.
type GhError struct {
	Msg     string
	Timeout bool
}

func (e *GhError) Error() string { return e.Msg }

// ExecGH runs the real gh binary (ISSUES_GH overrides the path).
type ExecGH struct{ Bin string }

func (g ExecGH) Run(args []string, stdin string, timeout time.Duration) (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		n := args
		if len(n) > 3 {
			n = n[:3]
		}
		return "", &GhError{fmt.Sprintf("timeout after %ds: gh %s", int(timeout.Seconds()), strings.Join(n, " ")), true}
	}
	if err != nil {
		if _, exited := err.(*exec.ExitError); !exited { // gh itself never ran: missing, or not executable
			return "", &GhError{Msg: "could not run gh (set ISSUES_GH or install the GitHub CLI)"}
		}
		m := trunc(pyStrip(se.String()+so.String()), 500)
		if m == "" {
			code := -1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			m = fmt.Sprintf("gh exit %d", code)
		}
		if len(args) > 1 && args[0] == "api" && args[1] == "graphql" { // the JSON body says which part failed
			return so.String(), &GhError{Msg: m}
		}
		return "", &GhError{Msg: m}
	}
	return so.String(), nil
}

func (a *App) gh(args []string, stdin string) (string, error) {
	return a.GH.Run(args, stdin, 90*time.Second)
}

// gql runs one GraphQL query and decodes data into out.
func (a *App) gql(query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	s, err := a.GH.Run([]string{"api", "graphql", "--input", "-"}, string(body), 120*time.Second)
	if err != nil {
		return err
	}
	var d struct {
		Data   json.RawMessage `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return err
	}
	if len(d.Errors) > 0 && string(d.Errors) != "null" && string(d.Errors) != "[]" {
		return &GhError{Msg: "graphql: " + trunc(string(d.Errors), 400)}
	}
	return json.Unmarshal(d.Data, out)
}
