package tools

import (
	"errors"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func inspectTestRange(startLine, endLine int) core.Range {
	return core.Range{
		Start: core.Position{Line: startLine, Column: 1},
		End: core.Position{Line: endLine, Column: 1},
	}
}

func TestFindInspectNodeChoosesInnermostScopeRegardlessOfLSPOrder(t *testing.T) {
	outer := symbolNode{Name: "outer", Kind: "class", Range: inspectTestRange(1, 20)}
	inner := symbolNode{Name: "inner", Kind: "function", Range: inspectTestRange(5, 9)}
	position := core.Position{Line: 7, Column: 2}
	for _, nodes := range [][]symbolNode{{outer, inner}, {inner, outer}} {
		got, err := findInspectNode(nodes, position, false)
		if err != nil || got.Name != "inner" {
			t.Fatalf("order-dependent scope selection: got=%q err=%v", got.Name, err)
		}
	}
}

func TestFindInspectNodeRejectsOverlappingUnrelatedScopes(t *testing.T) {
	// Flat LSP symbols can overlap without a parent/child relationship.
	// Returning the last one misleads an agent into editing the wrong body.
	a := symbolNode{Name: "a", Kind: "function", Range: inspectTestRange(1, 12)}
	b := symbolNode{Name: "b", Kind: "function", Range: inspectTestRange(5, 16)}
	for _, nodes := range [][]symbolNode{{a, b}, {b, a}} {
		_, err := findInspectNode(nodes, core.Position{Line: 7, Column: 2}, false)
		assertInspectAmbiguous(t, err)
	}
}

func TestFindInspectNodeAcceptsUniqueScopeNestedInsideOverlappingParents(t *testing.T) {
	a := symbolNode{Name: "a", Kind: "class", Range: inspectTestRange(1, 15)}
	b := symbolNode{Name: "b", Kind: "class", Range: inspectTestRange(4, 20)}
	leaf := symbolNode{Name: "leaf", Kind: "method", Range: inspectTestRange(6, 9)}
	got, err := findInspectNode([]symbolNode{a, b, leaf}, core.Position{Line: 7, Column: 2}, false)
	if err != nil || got.Name != "leaf" {
		t.Fatalf("unique nested scope: got=%q err=%v", got.Name, err)
	}
}

func TestFindInspectNodeRejectsDuplicateSelectionAndEqualScopes(t *testing.T) {
	position := core.Position{Line: 3, Column: 8}
	a := symbolNode{Name: "first", Kind: "function", Range: inspectTestRange(1, 10),
		SelectionRange: core.Range{Start: position, End: core.Position{Line: 3, Column: 13}}}
	b := symbolNode{Name: "second", Kind: "function", Range: inspectTestRange(1, 10),
		SelectionRange: core.Range{Start: position, End: core.Position{Line: 3, Column: 14}}}
	_, err := findInspectNode([]symbolNode{a, b}, position, true)
	assertInspectAmbiguous(t, err)
	_, err = findInspectNode([]symbolNode{a, b}, core.Position{Line: 5, Column: 2}, false)
	assertInspectAmbiguous(t, err)
}

func TestFindInspectNodePrefersUniqueExactSelectionOverEnclosingScopes(t *testing.T) {
	position := core.Position{Line: 7, Column: 2}
	a := symbolNode{Name: "a", Kind: "class", Range: inspectTestRange(1, 12)}
	b := symbolNode{Name: "b", Kind: "class", Range: inspectTestRange(5, 16)}
	selected := symbolNode{Name: "selected", Kind: "function", Range: inspectTestRange(7, 9),
		SelectionRange: core.Range{Start: position, End: core.Position{Line: 7, Column: 9}}}
	got, err := findInspectNode([]symbolNode{a, b, selected}, position, false)
	if err != nil || got.Name != "selected" {
		t.Fatalf("exact declaration should win: got=%q err=%v", got.Name, err)
	}
}

func assertInspectAmbiguous(t *testing.T, err error) {
	t.Helper()
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.AmbiguousSymbol {
		t.Fatalf("expected AMBIGUOUS_SYMBOL, got %v", err)
	}
}
