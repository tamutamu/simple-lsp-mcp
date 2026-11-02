package tools

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

func TestDecodeDocumentSymbolResponseDistinguishesFlatFromHierarchical(t *testing.T) {
	flatJSON := json.RawMessage(`[
		{"name":"Alpha","kind":12,"location":{"uri":"file:///workspace/example.go",
			"range":{"start":{"line":4,"character":5},"end":{"line":4,"character":10}}}}
	]`)
	tree, flat, hierarchical, err := decodeDocumentSymbolResponse(flatJSON)
	if err != nil || hierarchical || len(tree) != 0 || len(flat) != 1 {
		t.Fatalf("flat shape lost: tree=%#v flat=%#v hierarchical=%v err=%v", tree, flat, hierarchical, err)
	}
	if flat[0].Location.Range.Start.Line != 4 || flat[0].Location.Range.Start.Character != 5 {
		t.Fatalf("flat source location was lost: %#v", flat[0])
	}

	treeJSON := json.RawMessage(`[
		{"name":"Alpha","kind":12,
			"range":{"start":{"line":4,"character":0},"end":{"line":5,"character":0}},
			"selectionRange":{"start":{"line":4,"character":5},"end":{"line":4,"character":10}}}
	]`)
	tree, flat, hierarchical, err = decodeDocumentSymbolResponse(treeJSON)
	if err != nil || !hierarchical || len(flat) != 0 || len(tree) != 1 {
		t.Fatalf("hierarchical shape lost: tree=%#v flat=%#v hierarchical=%v err=%v", tree, flat, hierarchical, err)
	}
	if tree[0].Range.Start.Line != 4 || tree[0].SelectionRange.Start.Character != 5 {
		t.Fatalf("hierarchical source location was lost: %#v", tree[0])
	}
}

func TestDecodeDocumentSymbolResponseRejectsAmbiguousOrPartialShapes(t *testing.T) {
	cases := map[string]string{
		"mixed entries": `[{"name":"A","kind":12,"location":{}},
			{"name":"B","kind":12,"range":{},"selectionRange":{}}]`,
		"missing location and range": `[{"name":"A","kind":12}]`,
		"missing selection range": `[{"name":"A","kind":12,"range":{}}]`,
		"both shapes": `[{"name":"A","kind":12,"location":{},"range":{},"selectionRange":{}}]`,
		"null entry": `[null]`,
		"non-array": `{"name":"A","kind":12}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			tree, flat, _, err := decodeDocumentSymbolResponse(json.RawMessage(raw))
			var appErr *core.AppError
			if !errors.As(err, &appErr) || appErr.Code != core.IncompleteSearch {
				t.Fatalf("accepted malformed response: tree=%#v flat=%#v err=%v", tree, flat, err)
			}
			if len(tree) != 0 || len(flat) != 0 {
				t.Fatalf("partial response leaked: tree=%#v flat=%#v", tree, flat)
			}
		})
	}
}

func TestDecodeDocumentSymbolResponseAcceptsEmpty(t *testing.T) {
	for _, raw := range []string{`[]`, `null`} {
		tree, flat, hierarchical, err := decodeDocumentSymbolResponse(json.RawMessage(raw))
		if err != nil || !hierarchical || len(tree) != 0 || len(flat) != 0 {
			t.Fatalf("empty response %s: tree=%#v flat=%#v hierarchical=%v err=%v", raw, tree, flat, hierarchical, err)
		}
	}
}

func TestFlatSymbolsCannotBeMisreadAsDocumentSymbols(t *testing.T) {
	// Go's permissive Unmarshal is the regression's root cause: this succeeds
	// while inventing zero-valued ranges. The decoder must not take this path.
	raw := json.RawMessage(`[{"name":"Alpha","kind":12,
		"location":{"uri":"file:///example.go","range":{"start":{"line":8,"character":2},
		"end":{"line":8,"character":7}}}}]`)
	var wrong []protocol.DocumentSymbol
	if err := json.Unmarshal(raw, &wrong); err != nil {
		t.Fatal(err)
	}
	if len(wrong) != 1 || wrong[0].Range.Start.Line != 0 {
		t.Fatalf("regression fixture no longer demonstrates permissive decode: %#v", wrong)
	}
	_, flat, hierarchical, err := decodeDocumentSymbolResponse(raw)
	if err != nil || hierarchical || len(flat) != 1 || flat[0].Location.Range.Start.Line != 8 {
		t.Fatalf("flat location was not preserved: flat=%#v hierarchical=%v err=%v", flat, hierarchical, err)
	}
}
