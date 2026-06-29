package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// RunCerebrasAgent runs a complete conversation turn through Cerebras using
// native OpenAI function-calling against the full qmax tool set. It owns the
// whole multi-round tool loop (call → execute tools → feed results → repeat)
// and returns the final assistant text plus whether it succeeded.
//
// On any hard failure it returns ok=false. There is no Claude fallback here:
// when backend=cerebras the user has no Anthropic key, so we surface the error
// rather than silently switching providers.
func (a *Agent) RunCerebrasAgent(term *tui.Terminal) (string, bool) {
	if a.Cerebras == nil {
		return "", false
	}

	tools := toolDefsToOpenAI(a.tools)

	for iter := 0; iter < maxIterations; iter++ {
		// Keep history valid + bounded before each call (mirrors the Claude loop).
		session.SanitizeSessionMessages(a.History)
		a.compressHistory()

		system := a.buildSystemPrompt()
		msgs := historyToOpenAI(system, a.History)

		ctx, cancel := context.WithCancel(context.Background())
		a.cancelMu.Lock()
		a.cancel = cancel
		a.cancelMu.Unlock()

		term.StartThinking()
		resp, err := a.Cerebras.Chat(ctx, msgs, tools)
		term.StopThinking()

		a.cancelMu.Lock()
		a.cancel = nil
		a.cancelMu.Unlock()
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return "", false // interrupted
			}
			if a.Logger != nil {
				a.Logger.Error("cerebras", err.Error())
			}
			term.PrintError(fmt.Sprintf("Cerebras request failed: %v", err))
			return "", false
		}
		if len(resp.Choices) == 0 {
			term.PrintError("Cerebras returned no choices")
			return "", false
		}

		choice := resp.Choices[0]
		a.Usage.InputTokens += resp.Usage.PromptTokens
		a.Usage.OutputTokens += resp.Usage.CompletionTokens
		a.Usage.Requests++

		// Reconstruct the assistant turn as Anthropic-shaped blocks so the rest
		// of qmax (history compression, session persistence, cloud upload) sees
		// a uniform shape regardless of provider.
		var blocks []api.ContentBlock
		if choice.Message.Content != "" {
			blocks = append(blocks, api.ContentBlock{Type: "text", Text: choice.Message.Content})
		}
		for _, tc := range choice.Message.ToolCalls {
			var input interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				input = map[string]interface{}{}
			}
			blocks = append(blocks, api.ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, api.ContentBlock{Type: "text", Text: ""})
		}
		a.History = append(a.History, api.Message{Role: "assistant", Content: blocks})

		// Tool round: execute every requested tool, append results, loop.
		if len(choice.Message.ToolCalls) > 0 {
			if choice.Message.Content != "" {
				// PrintAssistant (not FinishMarkdown): this path is non-streaming,
				// so the streaming flag FinishMarkdown checks is never set and it
				// would silently drop the text.
				term.PrintAssistant(choice.Message.Content)
			}
			printCerebrasSpeed(term, resp, a.Cerebras.Model)

			var toolUse []api.ContentBlock
			for _, b := range blocks {
				if b.Type == "tool_use" {
					toolUse = append(toolUse, b)
					term.PrintToolIcon(b.Name)
					term.PrintToolStart(b.Name, b.Input)
				}
			}

			tctx, tcancel := context.WithCancel(context.Background())
			a.cancelMu.Lock()
			a.cancel = tcancel
			a.cancelMu.Unlock()
			results := a.executeToolCallsWithUI(toolUse, term, tctx)
			a.cancelMu.Lock()
			a.cancel = nil
			a.cancelMu.Unlock()
			tcancel()

			a.History = append(a.History, api.Message{Role: "user", Content: results})
			continue
		}

		// Final answer. PrintAssistant renders unconditionally (non-streaming path).
		term.PrintAssistant(choice.Message.Content)
		term.PrintTokenUsage(a.Usage)
		printCerebrasSpeed(term, resp, a.Cerebras.Model)
		return choice.Message.Content, true
	}

	return "", false
}

// printCerebrasSpeed surfaces the live Cerebras inference rate from a
// response's time_info object. This is the headline "Speed in Action" signal
// for the Cerebras + Gemma 4 demo — seeing ~2000+ tok/s in the terminal makes
// the latency advantage tangible. No-op when time_info is absent.
func printCerebrasSpeed(term *tui.Terminal, resp *oaiChatResponse, model string) {
	if resp == nil {
		return
	}
	tps := CerebrasTokensPerSecond(resp.TimeInfo)
	ttft := CerebrasTTFTSec(resp.TimeInfo)
	if tps <= 0 && ttft <= 0 {
		return
	}
	modelLabel := model
	if modelLabel == "" {
		modelLabel = "cerebras"
	}
	switch {
	case tps > 0 && ttft > 0:
		term.PrintSystem(fmt.Sprintf("⚡ %s on Cerebras: %.0f tok/s · TTFT %.2fs", modelLabel, tps, ttft))
	case tps > 0:
		term.PrintSystem(fmt.Sprintf("⚡ %s on Cerebras: %.0f tok/s", modelLabel, tps))
	default:
		term.PrintSystem(fmt.Sprintf("⚡ %s on Cerebras: TTFT %.2fs", modelLabel, ttft))
	}
}
