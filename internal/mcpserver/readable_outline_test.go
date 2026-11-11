package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestReadableOutlineKeepsEveryDisplayedSymbolAndHierarchy(t *testing.T) {
	symbols := []any{map[string]any{
		"name": "Store", "kind": "class", "symbol_id": "opaque-store", "line": 5,
		"children": []any{
			map[string]any{"name": "List", "kind": "method", "symbol_id": "opaque-list", "line": 18},
			map[string]any{"name": "Save", "kind": "method", "symbol_id": "opaque-save", "line": 47},
		},
	}, map[string]any{"name": "Read", "kind": "function", "symbol_id": "opaque-read", "line": 98}}
	input := map[string]any{"path": "pkg/store.ts", "detail": "outline"}
	data := map[string]any{"path": "pkg/store.ts", "symbols": symbols}
	text, ok := renderReadableResult("get_document_symbols", input, data)
	if !ok {
		t.Fatal("valid LSP outline was not rendered")
	}
	for _, expected := range []string{
		"LSP outline pkg/store.ts", "5 class Store", "18 method List", "47 method Save", "98 function Read",

		"[symbol_id=opaque-store]", "[symbol_id=opaque-list]", "[symbol_id=opaque-save]", "[symbol_id=opaque-read]",
		"inspect_symbol(symbol_id=", "some locals omitted",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("lost %s in %s", expected, text)
		}
	}
	if !strings.Contains(text, "  18 method List") {
		t.Fatal("child hierarchy lost")
	}
	if !strings.Contains(text, "\n  18 method List") {
		t.Fatal("readable outline must contain actual newline delimiters")
	}
	if _, ok := renderReadableResult("get_document_symbols", map[string]any{"detail": "full"}, data); ok {
		t.Fatal("explicit full detail must retain original JSON")
	}
}

func TestReadableOutlineFallsBackInsteadOfHidingFieldsOrIncompleteItems(t *testing.T) {
	valid := map[string]any{"path": "file.go", "symbols": []any{map[string]any{
		"name": "Load", "kind": "function", "line": 10, "symbol_id": "h",
	}}}
	corruptions := []func(map[string]any){
		func(m map[string]any) { m["warnings"] = []string{"partial"} },
		func(m map[string]any) {
			m["symbols"] = []any{map[string]any{"name": "Load", "kind": "function", "line": 10}}
		},
		func(m map[string]any) {
			m["symbols"] = []any{map[string]any{"name": "Load", "kind": "function", "line": 10, "symbol_id": "h", "signature": "foo"}}
		},
		func(m map[string]any) {
			m["symbols"] = []any{map[string]any{"name": "Load", "kind": "function", "line": 10, "symbol_id": "h", "children": []any{map[string]any{"name": "Inner"}}}}
		},
		func(m map[string]any) {
			m["symbols"] = []any{map[string]any{"name": "Load", "kind": "function", "line": 0, "symbol_id": "h"}}
		},
	}
	for i, corrupt := range corruptions {
		value := map[string]any{}
		for key, v := range valid {
			value[key] = v
		}
		corrupt(value)
		if text, ok := renderReadableResult("get_document_symbols", nil, value); ok {
			t.Errorf("invalid outline %d silently rendered: %s", i, text)
		}
	}
	// An empty but valid LSP outline must not be misread as a full index.
	text, ok := renderReadableResult("get_document_symbols", nil, map[string]any{"path": "empty.go", "symbols": []any{}})
	if !ok || !strings.Contains(text, "No displayed symbols") {
		t.Fatal(fmt.Sprint(text))
	}
}

func TestReadableSourceIncludesExactRangeAndFreshnessBoundary(t *testing.T) {
	// The code can include decorators or context preceding the selected range.
	source := map[string]any{
		"path": "api/handler.py", "name": "handler", "kind": "function",
		"range":           core.Range{Start: core.Position{Line: 20, Column: 1}, End: core.Position{Line: 25, Column: 1}},
		"code_start_line": 18, "view": "edit", "code": "@router.get('/')\ndef handler():\n    return True\n",
		"meta": core.Meta{Complete: true},
	}
	text, ok := renderReadableResult("inspect_symbol", map[string]any{"view": "edit"}, source)
	if !ok || !strings.Contains(text, "Declaration: api/handler.py:20-25") ||
		!strings.Contains(text, "Source snippet starts at api/handler.py:18") ||
		!strings.Contains(text, "avoid fetching overlapping file text") {
		t.Fatalf("location/context missing: %s", text)
	}
}
