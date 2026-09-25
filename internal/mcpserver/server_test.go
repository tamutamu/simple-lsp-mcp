package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefinitionsContainExactlyTheSpecifiedTools(t *testing.T) {
	ds := definitions()
	if len(ds) != 22 {
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
		"search_symbols":       "non-empty",
		"get_document_symbols": "prefer it over reading source text",
	} {
		if !strings.Contains(strings.ToLower(descriptions[name]), phrase) {
			t.Fatalf("%s description does not guide MCP selection: %q", name, descriptions[name])
		}
	}
}

func TestWorkspaceListingIsDistinctFromSearch(t *testing.T) {
	for _, d := range definitions() {
		if d.name != "list_workspace_symbols" {
			continue
		}
		if required, _ := d.schema["required"].([]string); len(required) != 0 {
			t.Fatal("workspace listing must not require a query or language")
		}
		props := d.schema["properties"].(map[string]any)
		if _, hasQuery := props["query"]; hasQuery {
			t.Fatal("workspace listing must not accept query")
		}
		if _, hasCursor := props["cursor"]; !hasCursor {
			t.Fatal("workspace listing needs pagination")
		}
		return
	}
	t.Fatal("workspace enumeration tool not registered")
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
	for _, name := range []string{"find_symbol", "get_symbol_context", "get_semantic_slice", "get_definition", "find_references", "get_hover", "impact_analysis", "rename_symbol"} {
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

func TestServerInstructionsGuideAgentTowardHighLevelTools(t *testing.T) {
	for _, phrase := range []string{"get_semantic_slice", "get_symbol_outline", "impact_analysis", "rename_symbol", "symbol_id", "INCOMPLETE_SEARCH"} {
		if !strings.Contains(serverInstructions, phrase) {
			t.Fatalf("server instructions missing %q: %s", phrase, serverInstructions)
		}
	}
	if len(serverInstructions) > 900 {
		t.Fatalf("server instructions are too verbose for model-visible initialization guidance: %d bytes", len(serverInstructions))
	}
}

func TestToolAnnotationsMarkNavigationReadOnlyAndOnboardAsWrite(t *testing.T) {
	for _, d := range definitions() {
		a := toolAnnotations(d.name)
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Fatalf("%s must be closed-world", d.name)
		}
		if d.name == "onboard" || d.name == "rename_symbol" {
			if a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
				t.Fatalf("%s annotations must declare an explicit write: %#v", d.name, a)
			}
			continue
		}
		if !a.ReadOnlyHint {
			t.Fatalf("%s must be marked read-only", d.name)
		}
	}
}
