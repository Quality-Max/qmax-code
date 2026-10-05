package repl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/tui"
)

const handoffUsage = "Usage: /handoff | /handoff reject <approach> | <evidence> | /handoff forget <number>"

// handleHandoffCommand returns whether the checkpoint changed so the REPL can
// persist it immediately without calling a model or starting a tool turn.
func handleHandoffCommand(input string, ag *agent.Agent, term *tui.Terminal) bool {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/handoff"))
	if args == "" {
		if context := ag.HandoffContext(); context != "" {
			term.PrintSystem(context)
		} else {
			term.PrintSystem("No rejected approaches recorded. Use /handoff reject <approach> | <evidence> before switching backends.")
		}
		return false
	}
	command, body, _ := strings.Cut(args, " ")
	var err error
	switch command {
	case "reject":
		approach, evidence, found := strings.Cut(body, "|")
		if !found {
			term.PrintError(handoffUsage)
			return false
		}
		err = ag.RejectApproach(approach, evidence)
	case "forget":
		number, parseErr := strconv.Atoi(strings.TrimSpace(body))
		if parseErr != nil {
			term.PrintError("Usage: /handoff forget <number> (see /handoff for entry numbers)")
			return false
		}
		err = ag.ForgetRejectedApproach(number)
	default:
		term.PrintError(handoffUsage)
		return false
	}
	if err != nil {
		term.PrintError(fmt.Sprintf("Handoff note unchanged: %v", err))
		return false
	}
	term.PrintSystem("Handoff checkpoint updated; it will accompany the next turn on any backend. Use /handoff to inspect it.")
	return true
}
