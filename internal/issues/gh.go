package issues

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GitHub is the only door to GitHub, in moon terms: what to read and what to send, never a command line.
// cliGH (gh_cli.go) speaks to the real gh; memGH (tests) is an in-memory GitHub.
type GitHub interface {
	// Reads that feed the mirror.
	PullIssues(repo string, q PullQuery) (RepoPull, error) // one page of issues (plus, on the first, labels, milestones and PRs)
	PullPRs(repo string, after string) (prPage, error)     // one page of pull requests
	Comments(nodeID string, after string) (commentPage, error)
	Issue(repo string, num int) (*issueNode, error)
	PullRequest(repo string, num int) (*prNode, error)

	// Issue writes, one REST call each.
	CreateIssue(repo string, in NewIssue) (int, error)
	EditIssue(repo, ident string, e IssueEdit) error
	CloseIssue(repo, ident, reason, comment string) error // reason: "completed" or "not planned"
	ReopenIssue(repo, ident string) error
	CommentIssue(repo, ident, body string) error
	// Lookups that make a retried write idempotent.
	FindIssueByMarker(repo, localID, since string) (int, error) // the issue whose body carries the local-id marker, or 0
	HasComment(repo, ident, marker string) (bool, error)

	// Batched writes: resolve node ids, then send aliased mutations in one request.
	ResolveIDs(repo string, q IDQuery) (IDs, error)
	Mutate(ms []mutation) (failed []string, err error) // failed[i] is why ms[i] was refused, "" when it went through

	// Label and milestone definitions.
	CreateLabel(repo string, l LabelSpec) error
	ListMilestones(repo string) ([]RemoteMilestone, error)
	CreateMilestone(repo string, m RemoteMilestone) (RemoteMilestone, error)
	UpdateMilestone(repo string, number int, ch MilestoneChange) error
}

// PullQuery asks for one page of a repo's issues. Since is "" for a full pull; First also asks for the labels,
// milestones and first page of pull requests.
type PullQuery struct {
	Since, After string
	First        bool
}

type labelNode struct {
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description *string `json:"description"`
}
type milestoneNode struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	State       string  `json:"state"`
	DueOn       *string `json:"dueOn"`
	Description string  `json:"description"`
	UpdatedAt   string  `json:"updatedAt"`
}
type issuePage struct {
	PageInfo pageInfo     `json:"pageInfo"`
	Nodes    []*issueNode `json:"nodes"`
}
type commentPage struct {
	PageInfo pageInfo  `json:"pageInfo"`
	Nodes    []comment `json:"nodes"`
}

// RepoPull is what one pull request returns; Labels and PullRequests are nil unless First was asked.
type RepoPull struct {
	Labels *struct {
		Nodes []labelNode `json:"nodes"`
	} `json:"labels"`
	Milestones struct {
		Nodes []milestoneNode `json:"nodes"`
	} `json:"milestones"`
	Issues       issuePage `json:"issues"`
	PullRequests *prPage   `json:"pullRequests"`
}

// NewIssue is a create; IssueEdit changes only the fields that are set.
type NewIssue struct {
	Title, Body, Milestone string
	Labels                 []string
}
type IssueEdit struct {
	Title, Body, Milestone  *string
	AddLabels, RemoveLabels []string
	RemoveMilestone         bool
}
type LabelSpec struct{ Name, Color, Description string }

// IDQuery names what a batch needs node ids for; IDs is the answer. Labels and milestones that GitHub does
// not have are simply missing. MilestonesAll says Milestones holds every milestone of the repo (not a first page).
type IDQuery struct {
	Issues, Labels []string // issue numbers, label names
	Milestones     bool
}
type IDs struct {
	Issues, Labels, Milestones map[string]string // number, name, title -> node id
	MilestonesAll              bool
}

// NewExecGH is the production GitHub: the gh binary at bin (empty: gh from PATH).
func NewExecGH(bin string) GitHub { return cliGH{run: ExecGH{Bin: bin}.Run} }

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
