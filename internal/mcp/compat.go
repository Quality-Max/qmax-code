package mcp

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qualitymax/qmax-code/internal/agent"
)

type compatibilityAlias struct {
	CloudName string
	Translate func(map[string]interface{}, int) (map[string]interface{}, error)
}

var compatibilityAliases = map[string]compatibilityAlias{
	"generate_test_code": {
		CloudName: "generate_code_for_test_case",
		Translate: func(args map[string]interface{}, _ int) (map[string]interface{}, error) {
			out := copyArgs(args)
			delete(out, "force")
			switch out["framework"] {
			case "go_test":
				out["framework"] = "go"
			case "rust_cargo":
				out["framework"] = "rust"
			}
			return out, nil
		},
	},
	"run_test": {
		CloudName: "run_tests",
		Translate: func(args map[string]interface{}, _ int) (map[string]interface{}, error) {
			scriptID, ok := numberString(args["script_id"])
			if !ok {
				return nil, fmt.Errorf("script_id is required")
			}
			out := copySelected(args, "base_url", "headless")
			out["script_ids"] = []string{scriptID}
			return out, nil
		},
	},
	"run_tests_batch": {
		CloudName: "run_tests",
		Translate: func(args map[string]interface{}, _ int) (map[string]interface{}, error) {
			out := copySelected(args, "base_url")
			ids, err := scriptIDList(args["script_ids"])
			if err != nil {
				return nil, err
			}
			out["script_ids"] = ids
			return out, nil
		},
	},
	"check_test_status": {
		CloudName: "get_execution",
		Translate: passthroughArgs,
	},
	"start_crawl": {
		CloudName: "start_ai_crawl",
		Translate: func(args map[string]interface{}, projectID int) (map[string]interface{}, error) {
			out := copyArgs(args)
			renameArg(out, "pages", "pages_limit")
			renameArg(out, "instructions", "custom_instructions")
			injectProjectID(out, projectID)
			return out, nil
		},
	},
	"start_crawl_from_test_case": {
		CloudName: "start_ai_crawl_from_test_case",
		Translate: passthroughArgs,
	},
	"crawl_status": {
		CloudName: "check_ai_crawl_status",
		Translate: passthroughArgs,
	},
	"crawl_results": {
		CloudName: "get_ai_crawl_results",
		Translate: passthroughArgs,
	},
	"list_crawl_jobs": {
		CloudName: "list_ai_crawl_jobs",
		Translate: func(args map[string]interface{}, projectID int) (map[string]interface{}, error) {
			out := copyArgs(args)
			injectProjectID(out, projectID)
			return out, nil
		},
	},
	"list_repos": {
		CloudName: "list_repositories",
		Translate: func(args map[string]interface{}, projectID int) (map[string]interface{}, error) {
			out := copyArgs(args)
			injectProjectID(out, projectID)
			return out, nil
		},
	},
	"review_repo": {
		CloudName: "ai_review",
		Translate: passthroughArgs,
	},
	"import_repo": {
		CloudName: "import_repository",
		Translate: func(args map[string]interface{}, _ int) (map[string]interface{}, error) {
			out := copyArgs(args)
			renameArg(out, "url", "repo_url")
			delete(out, "base_url")
			return out, nil
		},
	},
	"import_document": {
		CloudName: "import_test_cases_from_document",
		Translate: func(args map[string]interface{}, projectID int) (map[string]interface{}, error) {
			out := copyArgs(args)
			renameArg(out, "text", "text_content")
			injectProjectID(out, projectID)
			return out, nil
		},
	},
	"export_qtml": {
		CloudName: "export_project_as_qtml",
		Translate: func(args map[string]interface{}, projectID int) (map[string]interface{}, error) {
			out := copyArgs(args)
			injectProjectID(out, projectID)
			return out, nil
		},
	},
	"import_qtml": {
		CloudName: "import_qtml",
		Translate: func(args map[string]interface{}, projectID int) (map[string]interface{}, error) {
			out := copyArgs(args)
			renameArg(out, "content", "source")
			injectProjectID(out, projectID)
			return out, nil
		},
	},
}

func buildAuthenticatedToolList(cloudTools []toolDef) []toolDef {
	out := make([]toolDef, 0, len(cloudTools)+len(compatibilityAliases)+4)
	cloudNames := make(map[string]bool, len(cloudTools))
	for _, tool := range cloudTools {
		if agent.IsLocalTool(tool.Name) {
			continue
		}
		cloudNames[tool.Name] = true
		out = append(out, tool)
	}

	for _, local := range buildToolList(true) {
		out = append(out, local)
	}

	goDefs := make(map[string]toolDef)
	for _, tool := range buildToolList(false) {
		goDefs[tool.Name] = tool
	}
	aliasNames := make([]string, 0, len(compatibilityAliases))
	for aliasName := range compatibilityAliases {
		aliasNames = append(aliasNames, aliasName)
	}
	sort.Strings(aliasNames)
	for _, aliasName := range aliasNames {
		alias := compatibilityAliases[aliasName]
		if aliasName == alias.CloudName || cloudNames[aliasName] || !cloudNames[alias.CloudName] {
			continue
		}
		def, ok := goDefs[aliasName]
		if !ok {
			continue
		}
		def.Description = fmt.Sprintf("%s Compatibility alias for cloud tool %q.", def.Description, alias.CloudName)
		out = append(out, def)
	}
	return out
}

func translateCloudCall(name string, args map[string]interface{}, projectID int, cloudNames map[string]bool) (string, map[string]interface{}, error) {
	if cloudNames[name] {
		return name, args, nil
	}
	if alias, ok := compatibilityAliases[name]; ok {
		translated, err := alias.Translate(args, projectID)
		return alias.CloudName, translated, err
	}
	return name, args, nil
}

func passthroughArgs(args map[string]interface{}, _ int) (map[string]interface{}, error) {
	return copyArgs(args), nil
}

func copyArgs(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}

func copySelected(args map[string]interface{}, keys ...string) map[string]interface{} {
	out := make(map[string]interface{}, len(keys))
	for _, key := range keys {
		if value, ok := args[key]; ok {
			out[key] = value
		}
	}
	return out
}

func renameArg(args map[string]interface{}, from, to string) {
	if value, ok := args[from]; ok {
		args[to] = value
		delete(args, from)
	}
}

func injectProjectID(args map[string]interface{}, projectID int) {
	if _, ok := args["project_id"]; !ok && projectID > 0 {
		args["project_id"] = projectID
	}
}

func numberString(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		v = strings.TrimSpace(v)
		parsed, err := strconv.ParseInt(v, 10, 64)
		return strconv.FormatInt(parsed, 10), err == nil && parsed > 0
	case float64:
		return strconv.FormatInt(int64(v), 10), v > 0 && v == float64(int64(v))
	case int:
		return strconv.Itoa(v), v > 0
	case int64:
		return strconv.FormatInt(v, 10), v > 0
	default:
		return "", false
	}
}

func scriptIDList(value interface{}) ([]string, error) {
	var values []interface{}
	switch v := value.(type) {
	case string:
		for _, item := range strings.Split(v, ",") {
			values = append(values, strings.TrimSpace(item))
		}
	case []interface{}:
		values = v
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("script_ids is required")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("script_ids must be a comma-separated string or array")
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		id, ok := numberString(value)
		if !ok {
			return nil, fmt.Errorf("script_ids contains an invalid id")
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("script_ids is required")
	}
	return out, nil
}
