// Command moon is a thin dispatcher over the tools in internal/.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/your-moon/moon/internal/issues"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

const usage = `usage: moon <command> [args]

commands:
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
	case "issues":
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
