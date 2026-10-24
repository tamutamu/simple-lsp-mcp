package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var expectedTools = []string{
	"rename_symbol", "format_document", "get_code_actions", "apply_code_action", "get_diagnostics",
	"search_symbols", "list_workspace_symbols", "get_document_symbols", "get_hover",
	"get_definition", "find_references", "find_implementations", "get_type_definition",
	"get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes", "get_subtypes",
}

func TestMinimalLSPToolSurface(t *testing.T) {
	ds := definitions()
	if len(ds) != len(expectedTools) {
		t.Fatalf("got %d tools, want %d", len(ds), len(expectedTools))
	}
	seen := map[string]bool{}
	for i, d := range ds {
		if d.name != expectedTools[i] {
			t.Fatalf("tool[%d] = %q, want %q", i, d.name, expectedTools[i])
		}
		if seen[d.name] {
			t.Fatalf("duplicate tool: %s", d.name)
		}
		seen[d.name] = true
		if d.schema["type"] != "object" || d.call == nil {
			t.Fatalf("invalid definition: %s", d.name)
		}
	}
	for _, deleted := range []string{"find_symbol", "get_symbol_outline", "get_symbol_context", "get_semantic_slice", "impact_analysis", "verify_change", "get_symbol", "onboard"} {
		if seen[deleted] {
			t.Fatalf("obsolete tool remains: %s", deleted)
		}
	}
}

func TestToolCatalogStaysSmall(t *testing.T) {
	ds := definitions()
	catalog := make([]*mcp.Tool, 0, len(ds))
	for _, d := range ds {
		catalog = append(catalog, &mcp.Tool{Name: d.name, Description: d.description, InputSchema: d.schema, OutputSchema: objectSchema(), Annotations: toolAnnotations(d.name)})
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 18*1024 {
		t.Fatalf("MCP tool catalog uses %d bytes", len(encoded))
	}
}

func TestWorkspaceListingRemainsIndependentOfSearch(t *testing.T) {
	for _, d := range definitions() {
		if d.name != "list_workspace_symbols" {
			continue
		}
		if required, _ := d.schema["required"].([]string); len(required) != 0 {
			t.Fatal("workspace listing must not require query/language")
		}
		props := d.schema["properties"].(map[string]any)
		if _, ok := props["cursor"]; !ok {
			t.Fatal("missing pagination cursor")
		}
		if _, ok := props["query"]; ok {
			t.Fatal("workspace listing must not be a search")
		}
		return
	}
	t.Fatal("workspace listing removed")
}

func TestDirectTargetToolsRetainSymbolHandlesAndPaths(t *testing.T) {
	for _, name := range []string{"get_definition", "find_references", "find_implementations", "get_type_definition", "get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes", "get_subtypes", "get_hover", "rename_symbol"} {
		found := false
		for _, d := range definitions() {
			if d.name != name {
				continue
			}
			found = true
			p := d.schema["properties"].(map[string]any)
			for _, key := range []string{"symbol_id", "symbol_path", "path", "line", "column"} {
				if _, ok := p[key]; !ok {
					t.Fatalf("%s missing %s", name, key)
				}
			}
			for _, v := range append([]string(nil), func() []string { x, _ := d.schema["required"].([]string); return x }()...) {
				if v == "language" {
					t.Fatalf("%s still requires language", name)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestSimpleServerInstructions(t *testing.T) {
	if len(serverInstructions) > 650 {
		t.Fatalf("verbose instructions: %d", len(serverInstructions))
	}
	for _, deleted := range []string{"impact_analysis", "get_semantic_slice", "get_symbol_context", "verify_change"} {
		if strings.Contains(serverInstructions, deleted) {
			t.Fatalf("mandatory/composite instruction remains: %s", deleted)
		}
	}
	for _, wanted := range []string{"Do not invoke every tool", "rename_symbol", "tests"} {
		if !strings.Contains(serverInstructions, wanted) {
			t.Fatalf("instructions missing %q", wanted)
		}
	}
}

func TestWritesAreExplicitAndReadsStayReadOnly(t *testing.T) {
	writes := map[string]bool{"rename_symbol": true, "format_document": true, "apply_code_action": true}
	for _, d := range definitions() {
		a := toolAnnotations(d.name)
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Fatalf("open world: %s", d.name)
		}
		if writes[d.name] {
			if a.DestructiveHint == nil || !*a.DestructiveHint || a.ReadOnlyHint {
				t.Fatalf("missing write annotation: %s", d.name)
			}
		} else if !a.ReadOnlyHint {
			t.Fatalf("read tool unmarked: %s", d.name)
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
		t.Fatalf("unexpected content: %s", b)
	}
}

func TestEnumeratedInputsUseJSONSchemaEnums(t *testing.T) {
	all := allProperties()
	for _, name := range []string{"language", "kinds", "severities"} {
		spec := all[name].(map[string]any)
		schema := spec
		if name != "language" {
			schema = spec["items"].(map[string]any)
		}
		if len(schema["enum"].([]any)) == 0 {
			t.Fatalf("missing enum: %s", name)
		}
	}
}