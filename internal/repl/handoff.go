package repl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/tui"
)

const handoffUsage = `Usage: /handoff | /handoff reject <approach> | <evidence> | /handoff forget <number>  (write \| for a literal pipe in the approach)`

// handleHandoffCommand returns whether the checkpoint changed so the REPL can
// persist it immediately without calling a model or starting a tool turn.
func handleHandoffCommand(input string, ag *agent.Agent, term *tui.Terminal) bool {
	rest := strings.TrimPrefix(input, "/handoff")
	// Reject look-alikes such as "/handoffs" here: an unmatched slash command
	// would otherwise reach the model with its raw evidence.
	if rest != "" && !unicode.IsSpace(rune(rest[0])) {
		term.PrintError(handoffUsage)
		return false
	}
	args := strings.TrimSpace(rest)
	if args == "" {
		if context := ag.HandoffContext(); context != "" {
			term.PrintSystem(context)
		} else {
			term.PrintSystem("No rejected approaches recorded. Use /handoff reject <approach> | <evidence> before switching backends.")
		}
		return false
	}
	command, body := args, ""
	if i := strings.IndexFunc(args, unicode.IsSpace); i >= 0 {
		command, body = args[:i], args[i+1:]
	}
	var err error
	switch command {
	case "reject":
		approach, evidence, found := splitHandoffNote(body)
		if !found {
			term.PrintError(handoffUsage)
			return false
		}
		if err := ag.RejectApproach(approach, evidence); err != nil {
			term.PrintError(fmt.Sprintf("Handoff note unchanged: %v", err))
			return false
		}
		notes := ag.Conversation.Handoff.RejectedApproaches
		number, note := len(notes), notes[len(notes)-1]
		term.PrintSystem(fmt.Sprintf("Recorded rejected approach %d: %q. It will accompany the next turn on any backend.", number, note.Approach))
		if strings.Contains(note.Evidence, "|") {
			term.PrintSystem(fmt.Sprintf(`The evidence contains "|". If part of it belongs to the approach, run /handoff forget %d and write that pipe as \|.`, number))
		}
		return true
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

// splitHandoffNote splits at the first unescaped "|". Approaches can name shell
// pipelines, so a literal pipe there is written as \|; evidence keeps every
// later pipe verbatim because pasted output often contains them.
func splitHandoffNote(body string) (approach, evidence string, found bool) {
	for i := 0; i < len(body); i++ {
		switch {
		case body[i] == '\\' && i+1 < len(body) && body[i+1] == '|':
			i++
		case body[i] == '|':
			return strings.ReplaceAll(body[:i], `\|`, "|"), body[i+1:], true
		}
	}
	return "", "", false
}
