package main

import (
	"context"
	"fmt"
	"io"

	"github.com/qualitymax/qmax-code/internal/gate"
)

func runGateCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := gate.ParseCLIArgs(args, io.Discard)
	if err != nil {
		if gate.IsHelp(err) {
			gate.WriteCLIUsage(stdout)
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "qmax-code gate: %v\n", err)
		gate.WriteCLIUsage(stderr)
		return 2
	}

	result := gate.Run(ctx, gate.Options{Base: opts.Base, Dir: "."})
	gate.WriteText(stdout, result)
	return result.ExitCode()
}
