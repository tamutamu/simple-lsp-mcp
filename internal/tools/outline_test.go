package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestOutlinePreservesNavigationAndNestedDeclarations(t *testing.T) {
	leaf := map[string]any{
		"symbol_id": "function-1", "name": "handle", "kind": "method",
		"range":           core.Range{Start: core.Position{Line: 12}},
		"selection_range": core.Range{Start: core.Position{Line: 13}},
		"path":            "service.py", "language": "python",
		"children": []any{
			map[string]any{"name": "tmp", "kind": "variable", "symbol_id": "local-1", "range": core.Range{Start: core.Position{Line: 14}}},
			map[string]any{"name": "inner", "kind": "function", "symbol_id": "inner-1", "selection_range": core.Range{Start: core.Position{Line: 15}}},
		},
	}
	class := map[string]any{
		"symbol_id": "class-1", "name": "Service", "kind": "class",
		"selection_range": core.Range{Start: core.Position{Line: 10}},
		"children":        []any{leaf, map[string]any{"name": "field", "kind": "field", "symbol_id": "field-1"}},
	}
	visible := outlineDocumentSymbols([]any{
		map[string]any{"symbol_id": "router-1", "name": "router", "kind": "variable", "selection_range": core.Range{Start: core.Position{Line: 1}}},
		class,
	})
	if len(visible) != 2 {
		t.Fatalf("top-level symbols: %d, want 2", len(visible))
	}
	klass := visible[1].(map[string]any)
	methods := klass["children"].([]any)
	if len(methods) != 2 {
		t.Fatalf("class members: %#v", methods)
	}
	method := methods[0].(map[string]any)
	if methods[1].(map[string]any)["name"] != "field" {
		t.Fatalf("class field incorrectly omitted: %#v", methods)
	}
	if method["symbol_id"] != "function-1" || method["line"] != 13 {
		t.Fatalf("method navigation lost: %#v", method)
	}
	children := method["children"].([]any)
	if len(children) != 1 || children[0].(map[string]any)["name"] != "inner" {
		t.Fatalf("nested declarations: %#v", children)
	}
	encoded, err := json.Marshal(visible)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"selection_range", "language", "path", "tmp", "local-1"} {
		if strings.Contains(string(encoded), needle) {
			t.Fatalf("unexpected verbose/local field %q in outline: %s", needle, encoded)
		}
	}
}

func TestOutlineFlatSymbols(t *testing.T) {
	flat := []core.SymbolSummary{{SymbolID: "s1", Name: "Target", Kind: "function", Path: "example.go", SelectionRange: core.Range{Start: core.Position{Line: 21}}}}
	got := outlineFlatSymbols(flat)
	if len(got) != 1 || got[0].(map[string]any)["line"] != 21 || got[0].(map[string]any)["symbol_id"] != "s1" {
		t.Fatalf("flat outline lost semantic handle: %#v", got)
	}
}
