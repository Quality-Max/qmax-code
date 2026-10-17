// Command handoff-eval measures whether a coding backend repeats an approach
// that an earlier backend already ruled out, with and without a /handoff
// checkpoint. See README.md. Live runs call real coding CLIs and use their
// quota, so `run` only prints the plan unless --yes is given.
package main

import (
	"fmt"
	"os"

	"github.com/Quality-Max/qmax-code/internal/mcp"
)

const usage = `Usage:
  go run ./eval/handoff selfcheck             verify the fixture (no backends, no quota)
  go run ./eval/handoff run [flags]           print the run plan; add --yes to execute it
  go run ./eval/handoff summarize [--out dir] aggregate results into summary.md

Run "go run ./eval/handoff run -h" for run flags.`

func main() {
	// CLI backends spawn "<this binary> serve --mcp" for qmax's tools, exactly
	// as they spawn qmax-code itself in a normal session.
	if len(os.Args) > 2 && os.Args[1] == "serve" && os.Args[2] == "--mcp" {
		mcp.RunServer("handoff-eval")
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "selfcheck":
		err = selfCheck()
	case "run":
		err = runCommand(os.Args[2:])
	case "summarize":
		err = summarizeCommand(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "handoff-eval:", err)
		os.Exit(1)
	}
}
