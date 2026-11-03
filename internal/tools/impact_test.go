package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func testImpactSymbol(name string, line int) impactSymbol {
	start := core.Position{Line: line, Column: 1}
	return impactSymbol{
		Path: "src/"+name+".go",
		Node: symbolNode{
			Name: name, Kind: "function",
			Range: core.Range{Start: start, End: core.Position{Line: line+9, Column: 1}},
			SelectionRange: core.Range{Start: start, End: core.Position{Line: line, Column: 2}},
		},
	}
}

func TestImpactDepthControlsTransitiveExpansion(t *testing.T) {
	a, b, c := testImpactSymbol("A", 1), testImpactSymbol("B", 3), testImpactSymbol("C", 5)
	deps := map[string][]impactReference{
		a.key(): {{Owner: b, Line: 9}},
		b.key(): {{Owner: c, Line: 11}},
	}
	for _, tc := range []struct{
		depth, wantNodes, wantEdges, wantCalls int
		depthLimited bool
	}{
		{1, 2, 1, 1, true},
		{2, 3, 2, 2, true},
		{3, 3, 2, 3, false},
	} {
		calls := 0
		fetch := func(_ context.Context, s impactSymbol) ([]impactReference, int, error) {
			calls++
			return deps[s.key()], 0, nil
		}
		nodes, edges, limits, _, err := traverseImpact(context.Background(), a, tc.depth, 50, 100, fetch)
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != tc.wantNodes || len(edges) != tc.wantEdges || calls != tc.wantCalls {
			t.Fatalf("depth=%d: nodes=%d edges=%d calls=%d", tc.depth, len(nodes), len(edges), calls)
		}
		if got := len(limits) > 0; got != tc.depthLimited {
			t.Fatalf("depth=%d: limits=%#v, want limited=%v", tc.depth, limits, tc.depthLimited)
		}
		if tc.depth >= 2 && nodes[2].Depth != 2 {
			t.Fatalf("depth=%d: unexpected C level: %#v", tc.depth, nodes[2])
		}
	}
}

func TestImpactCycleNeverExpandsSameSymbolTwice(t *testing.T) {
	a, b, c := testImpactSymbol("A", 1), testImpactSymbol("B", 2), testImpactSymbol("C", 3)
	deps := map[string][]impactReference{
		a.key(): {{Owner: b, Line: 10}, {Owner: b, Line: 13}},
		b.key(): {{Owner: c, Line: 20}},
		c.key(): {{Owner: a, Line: 30}},
	}
	counts := map[string]int{}
	fetch := func(_ context.Context, s impactSymbol) ([]impactReference, int, error) {
		counts[s.key()]++
		return deps[s.key()], 0, nil
	}
	nodes, edges, limits, _, err := traverseImpact(context.Background(), a, 10, 50, 100, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 || len(edges) != 3 || len(limits) != 0 {
		t.Fatalf("cycle incorrectly expanded or truncated: nodes=%#v edges=%#v limits=%#v", nodes, edges, limits)
	}
	for _, s := range []impactSymbol{a, b, c} {
		if counts[s.key()] != 1 {
			t.Fatalf("symbol %s fetched %d times", s.Node.Name, counts[s.key()])
		}
	}
	if edges[0].ReferenceCount != 2 || len(edges[0].ReferenceLines) != 2 {
		t.Fatalf("repeated references not aggregated: %#v", edges[0])
	}
	if edges[2].From != "n2" || edges[2].To != "n0" {
		t.Fatalf("back edge lost: %#v", edges[2])
	}
}

func TestImpactNodeAndEdgeBudgetsNeverGiveCompleteResult(t *testing.T) {
	a, b, c := testImpactSymbol("A", 1), testImpactSymbol("B", 2), testImpactSymbol("C", 3)
	fetch := func(_ context.Context, s impactSymbol) ([]impactReference, int, error) {
		if s.key() == a.key() {
			return []impactReference{{Owner: b, Line: 9}, {Owner: c, Line: 11}}, 0, nil
		}
		return nil, 0, nil
	}
	nodes, edges, limits, _, err := traverseImpact(context.Background(), a, 5, 2, 100, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || len(edges) != 1 || !strings.Contains(strings.Join(limits, ","), "max_nodes") {
		t.Fatalf("node budget not enforced: nodes=%#v limits=%#v", nodes, limits)
	}
	nodes, edges, limits, _, err = traverseImpact(context.Background(), a, 5, 20, 1, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || len(nodes) != 2 || !strings.Contains(strings.Join(limits, ","), "max_edges") {
		t.Fatalf("edge budget left orphans or hid truncation: nodes=%#v edges=%#v limits=%#v", nodes, edges, limits)
	}
}

func TestImpactSymbolOnLineAvoidsGuessingAmbiguousColumn(t *testing.T) {
	first := symbolNode{Name: "A", Kind: "function", SelectionRange: core.Range{Start: core.Position{Line: 4, Column: 1}}}
	second := symbolNode{Name: "B", Kind: "function", SelectionRange: core.Range{Start: core.Position{Line: 4, Column: 20}}}
	if got, err := impactSymbolOnLine([]symbolNode{first}, 4); err != nil || got.Name != "A" {
		t.Fatalf("single declaration: %#v %v", got, err)
	}
	if _, err := impactSymbolOnLine([]symbolNode{first, second}, 4); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("ambiguous declaration was silently chosen: %v", err)
	}
	if _, err := impactSymbolOnLine([]symbolNode{first}, 99); err == nil {
		t.Fatal("non-declaration line silently matched")
	}
}

func TestImpactReferenceMapsToEnclosingCallableNotLocalVariable(t *testing.T) {
	fn := symbolNode{
		Name: "Handler", Kind: "function",
		Range: core.Range{Start: core.Position{Line: 1, Column: 1}, End: core.Position{Line: 10, Column: 1}},
		SelectionRange: core.Range{Start: core.Position{Line: 1, Column: 6}},
		Children: []symbolNode{{
			Name: "temporary", Kind: "variable",
			Range: core.Range{Start: core.Position{Line: 3, Column: 2}, End: core.Position{Line: 3, Column: 40}},
		}},
	}
	owner, found := impactEnclosingDeclaration([]symbolNode{fn}, core.Position{Line: 3, Column: 12})
	if !found || owner.Name != "Handler" {
		t.Fatalf("transitive caller lost to local variable: owner=%#v found=%v", owner, found)
	}
	if _, ok := impactEnclosingDeclaration([]symbolNode{fn}, core.Position{Line: 12, Column: 1}); ok {
		t.Fatal("reference outside declaration was wrongly assigned")
	}
}

func TestImpactUnmappedReferencesCannotClaimCompleteness(t *testing.T) {
	a := testImpactSymbol("A", 1)
	nodes, edges, limits, missing, err := traverseImpact(context.Background(), a, 3, 20, 20,
		func(_ context.Context, _ impactSymbol) ([]impactReference, int, error) {
			return nil, 1, nil
		})
	if err != nil || len(nodes) != 1 || len(edges) != 0 || len(limits) != 0 || missing != 1 {
		t.Fatalf("unmapped reference evidence lost: nodes=%#v edges=%#v limits=%#v unmapped=%d err=%v", nodes, edges, limits, missing, err)
	}
}
