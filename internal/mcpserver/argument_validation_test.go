package mcpserver

import (
	"math"
	"strings"
	"testing"
)

func toolInputSchema(t *testing.T, tool string, profile string) map[string]any {
	t.Helper()
	for _, d := range definitionsForProfile(profile) {
		if d.name == tool {
			return d.schema
		}
	}
	t.Fatalf("missing %s in %s", tool, profile)
	return nil
}

func TestRuntimeArgumentValidationMatchesAdvertisedJSONSchemas(t *testing.T) {
	tests := []struct {
		name, tool, profile string
		arguments           map[string]any
		reject              bool
		contains            string
	}{
		{"workspace without any arguments", "list_workspace_symbols", "full", map[string]any{}, false, ""},
		{"workspace unknown key", "list_workspace_symbols", "full", map[string]any{"lmit": 2}, true, "lmit"},
		{"search requires query", "search_symbols", "full", map[string]any{"language": "go"}, true, "query"},
		{"search requires language", "search_symbols", "full", map[string]any{"query": "Foo"}, true, "language"},
		{"valid search", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "limit": 5}, false, ""},
		{"wrong query type", "search_symbols", "full", map[string]any{"query": 2, "language": "go"}, true, "query"},
		{"bad language enum", "search_symbols", "full", map[string]any{"query": "Foo", "language": "ruby"}, true, "language"},
		{"limit fractional", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "limit": 2.9}, true, "limit"},
		{"limit boolean", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "limit": true}, true, "limit"},
		{"limit negative", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "limit": -1}, true, "limit"},
		{"limit zero", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "limit": 0}, true, "limit"},
		{"limit oversized float", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "limit": math.Exp2(64)}, true, "limit"},
		{"limit maximum float", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "limit": float64(math.MaxInt)}, true, "limit"},
		{"invalid kinds shape", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "kinds": "function"}, true, "kinds"},
		{"invalid kinds enum", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "kinds": []any{"FAKE"}}, true, "kinds[0]"},
		{"valid kinds list", "search_symbols", "full", map[string]any{"query": "Foo", "language": "go", "kinds": []any{"function", "struct"}}, false, ""},
		{"boolean string", "find_references", "full", map[string]any{"include_declaration": "true"}, true, "include_declaration"},
		{"boolean test filter", "find_references", "full", map[string]any{"exclude_test_files": "false"}, true, "exclude_test_files"},
		{"valid bool filter", "find_references", "full", map[string]any{"exclude_test_files": false}, false, ""},
		{"invalid view", "find_references", "full", map[string]any{"relation_view": "text"}, true, "relation_view"},
		{"valid view", "find_references", "full", map[string]any{"relation_view": "lines"}, false, ""},
		{"related string instead of array", "inspect_symbol", "full", map[string]any{"related": "references"}, true, "related"},
		{"related duplicate", "inspect_symbol", "full", map[string]any{"related": []any{"references", "references"}}, true, "related"},
		{"related empty", "inspect_symbol", "full", map[string]any{"related": []any{}}, true, "related"},
		{"related invalid value", "inspect_symbol", "full", map[string]any{"related": []any{"unknown"}}, true, "related[0]"},
		{"related valid", "inspect_symbol", "full", map[string]any{"related": []any{"references", "implementations"}}, false, ""},
		{"limit over 20", "inspect_symbol", "full", map[string]any{"relation_limit": 21}, true, "relation_limit"},
		{"limit noninteger", "inspect_symbol", "full", map[string]any{"relation_limit": 1.3}, true, "relation_limit"},
		{"limit valid", "inspect_symbol", "full", map[string]any{"relation_limit": 20}, false, ""},
		{"nested target missing fields allowed", "inspect_symbol", "full", map[string]any{"targets": []any{map[string]any{"symbol_path": "Greeter"}}}, false, ""},
		{"nested target nonobject", "inspect_symbol", "full", map[string]any{"targets": []any{"Greeter"}}, true, "targets[0]"},
		{"nested target bad line", "inspect_symbol", "full", map[string]any{"targets": []any{map[string]any{"line": "2"}}}, true, "targets[0].line"},
		{"nested target unknown key", "inspect_symbol", "full", map[string]any{"targets": []any{map[string]any{"symbol": "Greeter"}}}, true, "targets[0].symbol"},
		{"nested target empty list", "inspect_symbol", "full", map[string]any{"targets": []any{}}, true, "targets"},
		{"nested target too many", "inspect_symbol", "full", map[string]any{"targets": []any{1, 2, 3, 4, 5, 6, 7, 8, 9}}, true, "targets"},
		{"tabsize fractional", "format_document", "full", map[string]any{"path": "format.go", "tab_size": 4.3}, true, "tab_size"},
		{"format apply string", "format_document", "full", map[string]any{"path": "format.go", "apply": "true"}, true, "apply"},
		{"format apply bool", "format_document", "full", map[string]any{"path": "format.go", "apply": false}, false, ""},
		{"code action missing action id", "apply_code_action", "full", map[string]any{"path": "a.go", "line": 1, "column": 1}, true, "action_id"},
		{"code action id numeric", "apply_code_action", "full", map[string]any{"path": "a.go", "line": 1, "column": 1, "action_id": 42}, true, "action_id"},
		{"code action kinds numeric", "get_code_actions", "full", map[string]any{"path": "a.go", "line": 1, "column": 1, "code_action_kinds": []any{12}}, true, "code_action_kinds[0]"},
		{"coding diagnostics path required", "get_diagnostics", "coding", map[string]any{}, true, "path"},
		{"full diagnostics path optional", "get_diagnostics", "full", map[string]any{}, false, ""},
		{"diagnostics severity invalid", "get_diagnostics", "full", map[string]any{"severities": []any{"critical"}}, true, "severities[0]"},
		{"diagnostics severity valid", "get_diagnostics", "full", map[string]any{"severities": []any{"error", "warning"}}, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolArguments(toolInputSchema(t, tc.tool, tc.profile), tc.arguments)
			if (err != nil) != tc.reject {
				t.Fatalf("expected reject=%t; got err=%v; input=%#v", tc.reject, err, tc.arguments)
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error must identify field %q: %v", tc.contains, err)
			}
		})
	}
}

func TestArgumentValidationCannotWriteFromStringApplyFlag(t *testing.T) {
	schema := toolInputSchema(t, "rename_symbol", "full")
	if err := validateToolArguments(schema, map[string]any{
		"path": "models.go", "new_name": "NewPerson", "apply": "true",
	}); err == nil {
		t.Fatal("string 'true' was silently treated as apply=false; must reject")
	}
	if err := validateToolArguments(schema, map[string]any{
		"path": "models.go", "new_name": "NewPerson", "apply": true,
	}); err != nil {
		t.Fatalf("explicit apply=true unexpectedly rejected: %v", err)
	}
}
