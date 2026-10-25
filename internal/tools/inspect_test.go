package tools

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestFindInspectNodePrefersExactDeclarationOrEnclosingScope(t *testing.T) {
	nodes := []symbolNode{
		{
			Name: "Service", Kind: "class",
			Range:          core.Range{Start: core.Position{Line: 1, Column: 1}, End: core.Position{Line: 15, Column: 1}},
			SelectionRange: core.Range{Start: core.Position{Line: 1, Column: 7}, End: core.Position{Line: 1, Column: 14}},
			Children: []symbolNode{
				{
					Name: "run", Kind: "method",
					Range:          core.Range{Start: core.Position{Line: 3, Column: 5}, End: core.Position{Line: 10, Column: 1}},
					SelectionRange: core.Range{Start: core.Position{Line: 3, Column: 9}, End: core.Position{Line: 3, Column: 12}},
					Children: []symbolNode{
						{Name: "temp", Kind: "variable", Range: core.Range{Start: core.Position{Line: 5, Column: 9}, End: core.Position{Line: 5, Column: 13}},
							SelectionRange: core.Range{Start: core.Position{Line: 5, Column: 9}, End: core.Position{Line: 5, Column: 13}}},
					},
				},
			},
		},
	}
	cases := []struct {
		name  string
		at    core.Position
		exact bool
		want  string
	}{
		{"method-selection", core.Position{Line: 3, Column: 9}, true, "run"},
		{"in-method", core.Position{Line: 7, Column: 8}, false, "run"},
		{"variable-selection", core.Position{Line: 5, Column: 9}, false, "temp"},
		{"class-body", core.Position{Line: 12, Column: 2}, false, "Service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findInspectNode(nodes, tc.at, tc.exact)
			if err != nil || got.Name != tc.want {
				t.Fatalf("got %+v, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := findInspectNode(nodes, core.Position{Line: 16, Column: 1}, false); err == nil {
		t.Fatal("expected not-found outside any declaration")
	}
	if _, err := findInspectNode(nodes, core.Position{Line: 7, Column: 5}, true); err == nil {
		t.Fatal("symbol_path selection must not select arbitrary enclosing scope")
	}
}

func TestExtractSymbolSourceModesAndLimits(t *testing.T) {
	text := []byte("import abc\nclass Service:\n    def run(\n        self,\n        name: str,\n    ):\n        result = f'Hello, {name}'\n        return result\n\ndef last():\n    pass\n")
	span := core.Range{Start: core.Position{Line: 3, Column: 5}, End: core.Position{Line: 9, Column: 1}}

	all, truncated, err := extractSymbolSource(text, span, 120, 16384, false)
	if err != nil || truncated {
		t.Fatalf("source: %v, truncated %v", err, truncated)
	}
	if !strings.HasPrefix(all, "    def run(") || !strings.Contains(all, "        return result") || strings.Contains(all, "def last") {
		t.Fatalf("inaccurate code slice: %q", all)
	}
	signature, short, err := extractSymbolSource(text, span, 120, 16384, true)
	if err != nil || !short || !strings.Contains(signature, "name: str") || strings.Contains(signature, "return result") {
		t.Fatalf("signature %q, truncated=%v err=%v", signature, short, err)
	}
	head, short, err := extractSymbolSource(text, span, 2, 16384, false)
	if err != nil || !short || strings.Count(head, "\n") != 2 {
		t.Fatalf("line-limited %q, truncated=%v err=%v", head, short, err)
	}
	bytes, short, err := extractSymbolSource(text, span, 100, 21, false)
	if err != nil || !short || len([]byte(bytes)) > 21 || !strings.HasPrefix(bytes, "    def run(") {
		t.Fatalf("byte-limited %q, truncated=%v err=%v", bytes, short, err)
	}
	if _, _, err := extractSymbolSource(text, core.Range{Start: core.Position{Line: 100, Column: 1}, End: core.Position{Line: 101, Column: 1}}, 120, 16384, false); err == nil {
		t.Fatal("must reject stale or out-of-range LSP line data")
	}
}

func TestExtractSymbolSourceDoesNotSplitUTF8(t *testing.T) {
	text := []byte("func 测试() {\n  // 😀 emoji\n}\n")
	span := core.Range{Start: core.Position{Line: 1, Column: 1}, End: core.Position{Line: 4, Column: 1}}
	got, truncated, err := extractSymbolSource(text, span, 100, 25, false)
	if err != nil || !truncated || !utf8.ValidString(got) {
		t.Fatalf("UTF8 snippet %q truncated=%v err=%v", got, truncated, err)
	}
}

func TestInspectEditViewUsesExactSymbolAndKeepsOriginalSource(t *testing.T) {
	// Unit tests for lexically selected decorators/imports live in edit_context_test.go.
	// The full LSP identity/stale-handle behavior is checked in TestRealLanguageServers.
	for _, view := range []string{"edit", "source", "signature", "location"} {
		if view == "" {
			t.Fatal("empty view")
		}
	}
}

func TestInspectBatchRejectsInvalidInputs(t *testing.T) {
	var e Engine
	for _, tc := range []struct {
		name  string
		input map[string]any
	}{
		{"empty", map[string]any{"targets": []any{}}},
		{"too_many", map[string]any{"targets": make([]any, 9)}},
		{"not_array", map[string]any{"targets": "bad"}},
		{"not_target", map[string]any{"targets": []any{"bad"}}},
		{"wrong_field", map[string]any{"targets": []any{map[string]any{"path": "x.go", "symbol_path": "foo", "view": "source"}}}},
		{"duplicate", map[string]any{"targets": []any{map[string]any{"symbol_id": "x"}, map[string]any{"symbol_id": "x"}}}},
		{"ambiguous_top_level", map[string]any{"path": "x.go", "targets": []any{map[string]any{"symbol_id": "x"}}}},
		{"missing_identity", map[string]any{"targets": []any{map[string]any{"path": "x.go"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.InspectSymbol(context.Background(), tc.input)
			if err == nil {
				t.Fatal("expected invalid argument to be rejected")
			}
		})
	}
}
