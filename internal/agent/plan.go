package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qualitymax/qmax-code/internal/tui"
)

// PlanStep is a single entry in the agent's task plan, surfaced via update_plan.
type PlanStep struct {
	Title  string
	Status string // pending | in_progress | done
}

var validPlanStatus = map[string]bool{"pending": true, "in_progress": true, "done": true}

// maxPlanSteps mirrors the maxItems cap in the update_plan input schema. Enforced
// here too because a model can ignore the schema and the API doesn't reject it.
const maxPlanSteps = 20

// parsePlanSteps extracts and validates the steps array from update_plan input.
func parsePlanSteps(rawInput interface{}) ([]PlanStep, error) {
	input := parseInput(rawInput)
	raw, ok := input["steps"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("steps is required")
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("steps must be an array")
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("steps must contain at least one step")
	}
	if len(list) > maxPlanSteps {
		return nil, fmt.Errorf("too many steps (%d); max %d", len(list), maxPlanSteps)
	}
	steps := make([]PlanStep, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("step %d must be an object", i+1)
		}
		// Require a real string (nil/number/bool/object all fail) rather than
		// stringifying whatever the model sent — a non-string title is a schema
		// violation, not a label to coerce into "42" or "map[...]".
		title, ok := m["title"].(string)
		if !ok {
			return nil, fmt.Errorf("step %d: title is required and must be a string", i+1)
		}
		if title = strings.TrimSpace(title); title == "" {
			return nil, fmt.Errorf("step %d: title is required", i+1)
		}
		// Default an omitted/empty status to "pending" — a model often sends new
		// steps without one. A non-string or unknown value is a real error.
		status := "pending"
		if raw, present := m["status"]; present && raw != nil {
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("step %d: status must be a string", i+1)
			}
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				status = trimmed
			}
		}
		if !validPlanStatus[status] {
			return nil, fmt.Errorf("step %d: invalid status %q (want pending|in_progress|done)", i+1, status)
		}
		steps = append(steps, PlanStep{Title: title, Status: status})
	}
	return steps, nil
}

// executeUpdatePlan validates the plan and returns a compact JSON summary for the
// model. The human-facing checklist is rendered separately by the UI layer, so
// the model-facing result stays small (it can re-derive the plan from its own
// prior tool call).
func executeUpdatePlan(rawInput interface{}) string {
	steps, err := parsePlanSteps(rawInput)
	if err != nil {
		return jsonError(err.Error())
	}
	done := 0
	for _, s := range steps {
		if s.Status == "done" {
			done++
		}
	}
	out, _ := json.Marshal(map[string]interface{}{
		"ok":    true,
		"total": len(steps),
		"done":  done,
	})
	return string(out)
}

// toTUIPlanSteps converts agent plan steps into the tui package's render shape.
// (tui can't import agent — agent imports tui — so the types stay separate.)
func toTUIPlanSteps(steps []PlanStep) []tui.PlanStep {
	out := make([]tui.PlanStep, len(steps))
	for i, s := range steps {
		out[i] = tui.PlanStep{Title: s.Title, Status: s.Status}
	}
	return out
}
