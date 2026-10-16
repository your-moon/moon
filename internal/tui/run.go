package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/your-moon/moon/internal/issues"
)

// Run starts the dashboard for `moon tui` or `moon issues tui`. args are the flags after the command:
// --config FILE, --dir MIRROR, --agent NAME. It returns the process exit status.
func Run(args []string, e issues.Env) int {
	var cfg, dir, agent string
	for i := 0; i < len(args); i++ {
		k, v, hv := strings.Cut(args[i], "=")
		switch k {
		case "--config", "--dir", "--agent":
			if !hv {
				if i+1 >= len(args) {
					fmt.Fprintf(e.Err, "%s needs a value\n", k)
					return 2
				}
				i++
				v = args[i]
			}
			switch k {
			case "--config":
				cfg = v
			case "--dir":
				dir = v
			default:
				agent = v
			}
		default:
			fmt.Fprintf(e.Err, "usage: moon tui [--config FILE] [--dir MIRROR] [--agent NAME]\n")
			return 2
		}
	}
	if agent == "" {
		agent = e.Getenv("USER")
	}
	if agent == "" {
		agent = "user"
	}
	if e.Getenv("NO_COLOR") != "" || e.Getenv("TERM") == "dumb" {
		lipgloss.SetColorProfile(termenv.Ascii) // plain text and the ASCII progress bar
	}
	var model tea.Model
	if sess, err := issues.NewSession(cfg, dir, e); err != nil {
		model = ErrorShell(err.Error())
	} else {
		model = NewShell([]Tab{
			NewIssuesTab(sess, Options{Agent: agent, Editor: e.Getenv("EDITOR"), MirrorDir: sess.MirrorDir()}),
			NewMilestonesTab(sess),
			NewClaimsTab(sess),
		}, 0)
	}
	if _, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		fmt.Fprintln(e.Err, "moon tui:", err)
		return 1
	}
	return 0
}
