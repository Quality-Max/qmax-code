package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestParsePlanSteps_Valid(t *testing.T) {
	in := map[string]interface{}{
		"steps": []interface{}{
			map[string]interface{}{"title": "Generate test", "status": "done"},
			map[string]interface{}{"title": "Run it", "status": "in_progress"},
			map[string]interface{}{"title": "Heal failures", "status": "pending"},
		},
	}
	steps, err := parsePlanSteps(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(steps))
	}
	if steps[1].Title != "Run it" || steps[1].Status != "in_progress" {
		t.Errorf("step[1] = %+v, want {Run it in_progress}", steps[1])
	}
}

func TestParsePlanSteps_DefaultsStatusToPending(t *testing.T) {
	in := map[string]interface{}{
		"steps": []interface{}{
			map[string]interface{}{"title": "No status field"},
			map[string]interface{}{"title": "Empty status", "status": ""},
		},
	}
	steps, err := parsePlanSteps(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, s := range steps {
		if s.Status != "pending" {
			t.Errorf("step[%d].Status = %q, want pending", i, s.Status)
		}
	}
}

func TestParsePlanSteps_Errors(t *testing.T) {
	tooMany := make([]interface{}, maxPlanSteps+1)
	for i := range tooMany {
		tooMany[i] = map[string]interface{}{"title": "x", "status": "pending"}
	}
	cases := []struct {
		name string
		in   map[string]interface{}
		want string
	}{
		{"missing steps", map[string]interface{}{}, "required"},
		{"steps not array", map[string]interface{}{"steps": "nope"}, "must be an array"},
		{"empty steps", map[string]interface{}{"steps": []interface{}{}}, "at least one"},
		{"too many", map[string]interface{}{"steps": tooMany}, "too many"},
		{"step not object", map[string]interface{}{"steps": []interface{}{"x"}}, "must be an object"},
		{"missing title", map[string]interface{}{"steps": []interface{}{map[string]interface{}{"status": "pending"}}}, "title is required"},
		{"bad status", map[string]interface{}{"steps": []interface{}{map[string]interface{}{"title": "t", "status": "doing"}}}, "invalid status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePlanSteps(tc.in)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestExecuteUpdatePlan_Summary(t *testing.T) {
	in := map[string]interface{}{
		"steps": []interface{}{
			map[string]interface{}{"title": "a", "status": "done"},
			map[string]interface{}{"title": "b", "status": "done"},
			map[string]interface{}{"title": "c", "status": "pending"},
		},
	}
	out := executeUpdatePlan(in)
	var got struct {
		OK    bool `json:"ok"`
		Total int  `json:"total"`
		Done  int  `json:"done"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%s)", err, out)
	}
	if !got.OK || got.Total != 3 || got.Done != 2 {
		t.Errorf("got %+v, want {ok:true total:3 done:2}", got)
	}
}

func TestExecuteUpdatePlan_InvalidReturnsError(t *testing.T) {
	out := executeUpdatePlan(map[string]interface{}{})
	if !strings.Contains(out, `"error"`) {
		t.Errorf("expected JSON error, got %s", out)
	}
}

func TestUpdatePlan_ExposedToNativeAgentButNotMCP(t *testing.T) {
	if !hasTool(BuildToolDefs(), "update_plan") {
		t.Error("update_plan missing from BuildToolDefs (native agent)")
	}
	if hasTool(BuildMCPToolDefs(), "update_plan") {
		t.Error("update_plan must NOT be exposed via BuildMCPToolDefs (collides with Claude Code's TodoWrite)")
	}
}

func hasTool(defs []api.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}
