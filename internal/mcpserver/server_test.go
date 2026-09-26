package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDefinitionsContainExactlyTheSpecifiedTools(t *testing.T) {
	want := []string{
		"find_symbol", "get_symbol_outline", "get_symbol_context", "get_semantic_slice", "impact_analysis",
		"rename_symbol", "format_document", "get_code_actions", "apply_code_action", "get_diagnostics",
		"search_symbols", "list_workspace_symbols", "get_document_symbols", "get_symbol", "get_hover",
		"get_definition", "find_references", "find_implementations", "get_type_definition", "get_declaration",
		"get_incoming_calls", "get_outgoing_calls", "get_supertypes", "get_subtypes", "onboard",
	}
	ds := definitions()
	if len(ds) != len(want) {
		t.Fatalf("got %d tools, want %d", len(ds), len(want))
	}
	for i, d := range ds {
		if d.name != want[i] {
			t.Fatalf("tool[%d] = %q, want %q", i, d.name, want[i])
		}
		if d.schema["type"] != "object" {
			t.Fatalf("%s lacks object schema", d.name)
		}
	}
}

func TestToolCatalogStaysWithinPromptBudget(t *testing.T) {
	const maxBytes = 25 * 1024
	catalog := make([]*mcp.Tool, 0, len(definitions()))
	for _, d := range definitions() {
		catalog = append(catalog, &mcp.Tool{
			Name: d.name, Description: d.description, InputSchema: d.schema,
			OutputSchema: objectSchema(), Annotations: toolAnnotations(d.name),
		})
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxBytes {
		t.Fatalf("MCP tool catalog is %d bytes; budget is %d bytes. Keep the full API, but compact metadata before growing the model-visible surface.", len(encoded), maxBytes)
	}
}

func TestPreferredAgentWorkflowToolsComeFirstWithoutRemovingPrimitives(t *testing.T) {
	wantPrefix := []string{
		"find_symbol", "get_symbol_outline", "get_symbol_context", "get_semantic_slice", "impact_analysis",
		"rename_symbol", "format_document", "get_code_actions", "apply_code_action", "get_diagnostics",
	}
	ds := definitions()
	for i, want := range wantPrefix {
		if ds[i].name != want {
			t.Fatalf("preferred tool %d = %q, want %q", i, ds[i].name, want)
		}
	}
	for _, primitive := range []string{"get_definition", "find_references", "find_implementations", "get_type_definition", "get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes", "get_subtypes"} {
		found := false
		for _, d := range ds {
			if d.name == primitive {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("required low-level primitive %q was removed", primitive)
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
	for _, phrase := range []string{"get_symbol_context", "get_semantic_slice", "get_symbol_outline", "impact_analysis", "symbol_id", "INCOMPLETE_SEARCH"} {
		if !strings.Contains(serverInstructions, phrase) {
			t.Fatalf("server instructions missing %q: %s", phrase, serverInstructions)
		}
	}
	if len(serverInstructions) > 900 {
		t.Fatalf("server instructions are too verbose for model-visible initialization guidance: %d bytes", len(serverInstructions))
	}
}

func TestToolAnnotationsMarkReadsAndExplicitWrites(t *testing.T) {
	for _, d := range definitions() {
		a := toolAnnotations(d.name)
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Fatalf("%s must be closed-world", d.name)
		}
		if d.name == "onboard" || d.name == "rename_symbol" || d.name == "format_document" || d.name == "apply_code_action" {
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

func TestEnumeratedInputsUseJSONSchemaEnums(t *testing.T) {
	props := allProperties()
	languageSchema, _ := props["language"].(map[string]any)
	if values, ok := languageSchema["enum"].([]any); !ok || len(values) == 0 {
		t.Fatalf("language schema must advertise enum values: %#v", languageSchema)
	}
	for _, name := range []string{"include", "kinds", "severities"} {
		schema, _ := props[name].(map[string]any)
		items, _ := schema["items"].(map[string]any)
		if values, ok := items["enum"].([]any); !ok || len(values) == 0 {
			t.Fatalf("%s item schema must advertise enum values: %#v", name, schema)
		}
	}
}
