package gate

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// CLIOptions are the stable arguments for the gate subcommand.
type CLIOptions struct {
	Base string
}

// ParseCLIArgs parses qmax-code gate arguments without touching global flags.
func ParseCLIArgs(args []string, errorOutput io.Writer) (CLIOptions, error) {
	flags := flag.NewFlagSet("gate", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	base := flags.String("base", DefaultBase, "Git ref to compare HEAD against")
	flags.Usage = func() { WriteCLIUsage(errorOutput) }
	if err := flags.Parse(args); err != nil {
		return CLIOptions{}, err
	}
	if flags.NArg() != 0 {
		return CLIOptions{}, fmt.Errorf("unexpected gate argument %q", flags.Arg(0))
	}
	if err := validateBase(*base); err != nil {
		return CLIOptions{}, err
	}
	return CLIOptions{Base: *base}, nil
}

// WriteCLIUsage prints only non-sensitive static command help.
func WriteCLIUsage(w io.Writer) {
	const usage = `Usage: qmax-code gate [--base REF]

Runs the local, read-only PR quality gate.
Exit codes: 0 PASS, 1 FAIL, 2 INCOMPLETE or usage error.
`
	_, _ = io.WriteString(w, usage)
}

func IsHelp(err error) bool {
	return errors.Is(err, flag.ErrHelp)
}
