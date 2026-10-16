// Command moon is a thin dispatcher over the tools in internal/.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/your-moon/moon/internal/issues"
	"github.com/your-moon/moon/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

const usage = `usage: moon <command> [args]

commands:
  tui       interactive terminal dashboard: issues, claims (press ? inside for keys)
  issues    local-first mirror of GitHub issues and pull requests (moon issues help)
  version   print the version
  help      print this text
`

func main() {
	cwd, _ := os.Getwd()
	os.Exit(run(os.Args[1:], issues.Env{
		Getenv: os.Getenv, Stdin: os.Stdin, Out: os.Stdout, Err: os.Stderr, Cwd: cwd,
	}))
}

func run(args []string, e issues.Env) int {
	if len(args) == 0 {
		fmt.Fprint(e.Out, usage)
		return 0
	}
	switch args[0] {
	case "tui":
		return tui.Run(args[1:], e)
	case "issues":
		if rest, ok := issuesTUIArgs(args[1:]); ok {
			return tui.Run(rest, e)
		}
		return issues.Main(args[1:], e)
	case "version", "--version":
		fmt.Fprintln(e.Out, "moon", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(e.Out, usage)
		return 0
	}
	fmt.Fprintf(e.Err, "moon: unknown command %q\n\n", args[0])
	io.WriteString(e.Err, usage)
	return 2
}

// issuesTUIArgs recognizes `moon issues [--config F] [--dir D] tui [flags]` and returns every flag.
func issuesTUIArgs(args []string) ([]string, bool) {
	var flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "tui":
			return append(flags, args[i+1:]...), true
		case a == "--config" || a == "--dir":
			if i+1 >= len(args) {
				return nil, false
			}
			flags = append(flags, a, args[i+1])
			i++
		case strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "--dir="):
			flags = append(flags, a)
		default:
			return nil, false
		}
	}
	return nil, false
}
