package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefinitionsContainExactlyTheSpecifiedTools(t *testing.T) {
	ds := definitions()
	if len(ds) != 15 {
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
