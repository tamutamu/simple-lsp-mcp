package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefinitionsContainExactlyTheSpecifiedTools(t *testing.T) {
	ds := definitions()
	if len(ds) != 21 {
		t.Fatalf("got %d tools", len(ds))
	}
	seen := map[string]bool{}
	for _, d := range ds {
		if seen[d.name] {
			t.Fatalf("duplicate %q", d.name)
		}
		seen[d.name] = true
		if d.schema["type"] != "object" {
			t.Fatalf("%s lacks object schema", d.name)
		}
	}
}

func TestDiscoveryToolDescriptionsGuideCodeInvestigation(t *testing.T) {
	descriptions := map[string]string{}
	for _, definition := range definitions() {
		descriptions[definition.name] = definition.description
	}
	for name, phrase := range map[string]string{
		"search_symbols":         "before any shell search",
		"list_workspace_symbols": "list methods in go code",
		"get_document_symbols":   "prefer it over reading source text",
	} {
		if !strings.Contains(strings.ToLower(descriptions[name]), phrase) {
			t.Fatalf("%s description does not guide MCP selection: %q", name, descriptions[name])
		}
	}
	for _, definition := range definitions() {
		if definition.name != "list_workspace_symbols" {
			continue
		}
		required := definition.schema["required"].([]string)
		if len(required) != 1 || required[0] != "language" {
			t.Fatalf("list_workspace_symbols required = %#v, want language", required)
		}
		properties := definition.schema["properties"].(map[string]any)
		if properties["kinds"].(map[string]any)["type"] != "array" {
			t.Fatalf("list_workspace_symbols kinds schema = %#v", properties["kinds"])
		}
	}
}
func TestTargetToolsAcceptSymbolPath(t *testing.T) {
	for _, name := range []string{"get_definition", "find_references", "find_implementations", "get_type_definition", "get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes", "get_subtypes", "get_hover"} {
		found := false
		for _, d := range definitions() {
			if d.name != name {
				continue
			}
			found = true
			properties := d.schema["properties"].(map[string]any)
			if _, ok := properties["symbol_path"]; !ok {
				t.Fatalf("%s schema lacks symbol_path: %#v", name, properties)
			}
		}
		if !found {
			t.Fatalf("tool %q not found", name)
		}
	}
}

func TestTargetToolsInferLanguage(t *testing.T) {
	for _, name := range []string{"find_symbol", "get_symbol_context", "get_semantic_slice", "get_definition", "find_references", "get_hover", "impact_analysis"} {
		for _, d := range definitions() {
			if d.name != name {
				continue
			}
			required, _ := d.schema["required"].([]string)
			for _, field := range required {
				if field == "language" {
					t.Fatalf("%s still requires language", name)
				}
			}
		}
	}
}

func TestResultStructuredAndTextContentMatch(t *testing.T) {
	v := map[string]any{"symbols": []string{"one"}}
	r := result(v, false)
	if r.StructuredContent == nil || len(r.Content) != 1 {
		t.Fatal("missing result content")
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"symbols":["one"]}` {
		t.Fatalf("unexpected structured content: %s", b)
	}
}
