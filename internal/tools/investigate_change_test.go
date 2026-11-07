package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestImpactSymbolOnPositionDisambiguatesSameLine(t *testing.T) {
	nodes := []symbolNode{
		{Name: "one", SelectionRange: core.Range{Start: core.Position{Line: 9, Column: 3}}},
		{Name: "two", SelectionRange: core.Range{Start: core.Position{Line: 9, Column: 22}}},
	}
	if _, err := impactSymbolOnLine(nodes, 9); err == nil {
		t.Fatal("line alone must remain ambiguous")
	}
	node, err := impactSymbolOnPosition(nodes, core.Position{Line: 9, Column: 22})
	if err != nil || node.Name != "two" {
		t.Fatalf("exact same-line selection failed: %#v: %v", node, err)
	}
	if _, err := impactSymbolOnPosition(nodes, core.Position{Line: 9, Column: 21}); err == nil {
		t.Fatal("missing exact column was silently guessed")
	}
}

func TestSummarizeChangeInvestigationGroupsBoundedEvidence(t *testing.T) {
	found := map[string]any{"path": "lib/root.go", "name": "Target", "kind": "function"}
	nodes := []impactNode{
		{ID: "n0", Path: "lib/root.go", Name: "Target", Line: 8, Kind: "function", Depth: 0},
		{ID: "n1", Path: "pkg/b.go", Name: "B", Line: 44, Kind: "function", Depth: 1},
		{ID: "n2", Path: "pkg/a.go", Name: "A", Line: 10, Kind: "function", Depth: 1},
		{ID: "n3", Path: "pkg/a.go", Name: "C", Line: 6, Kind: "function", Depth: 2},
	}
	edges := []impactEdge{
		{From: "n0", To: "n1", ReferenceCount: 2},
		{From: "n0", To: "n2", ReferenceCount: 1},
		{From: "n1", To: "n3", ReferenceCount: 3},
	}
	graph := map[string]any{
		"root": "n0", "nodes": nodes, "edges": edges, "depth": 2,
		"unmapped_references": 0,
		"meta": map[string]any{"complete": false, "truncated": true,
			"limit_reasons": []string{"depth"}, "note": "dynamic calls may be missed"},
	}
	result, err := summarizeChangeInvestigation(found, graph, "files")
	if err != nil {
		t.Fatal(err)
	}
	if result["affected_file_count"] != 2 || result["affected_declarations"] != 3 ||
		result["direct_reference_count"] != 3 || result["traversed_reference_count"] != 6 {
		t.Fatalf("incorrect counts: %#v", result)
	}
	if _, present := result["graph"]; present {
		t.Fatal("default summary leaks full graph")
	}
	if metaComplete(result["meta"].(map[string]any)) {
		t.Fatal("truncated graph claimed complete")
	}
	// Marshal through JSON to assert stable, sorted exported presentation.
	encoded, err := json.Marshal(result["affected_files"])
	if err != nil {
		t.Fatal(err)
	}
	var files []struct {
		Path         string `json:"path"`
		Count        int    `json:"count"`
		Declarations []struct {
			Name string `json:"name"`
			Line int    `json:"line"`
		} `json:"declarations"`
	}
	if err := json.Unmarshal(encoded, &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "pkg/a.go" || files[1].Path != "pkg/b.go" || files[0].Declarations[0].Name != "C" {
		t.Fatalf("wrong sorting: %#v", files)
	}
}

func TestSummarizeChangeInvestigationRejectsBrokenGraph(t *testing.T) {
	_, err := summarizeChangeInvestigation(map[string]any{"path": "test.go"}, map[string]any{
		"nodes": []impactNode{}, "edges": []impactEdge{}, "root": "nope",
	}, "files")
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("broken graph should fail closed: %v", err)
	}
}
func TestInvestigateChangesBatchRejectsMalformedAndDuplicateTargets(t *testing.T) {
	engine := &Engine{}
	ctx := context.Background()
	one := map[string]any{"path": "sample.ts", "symbol_path": "target"}
	cases := []map[string]any{
		{"targets": []any{one}},
		{"targets": []any{one, one}},
		{"targets": []any{one, map[string]any{"path": "other.ts", "symbol_path": "second"}}, "path": "sample.ts"},
		{"targets": []any{one, map[string]any{"path": "other.ts", "symbol_path": "second", "unknown": "value"}}},
		{"targets": []any{one, map[string]any{"path": "other.ts", "symbol_path": "second"}}, "detail": "graph"},
		{"targets": []any{one, map[string]any{"path": "other.ts", "symbol_path": "second"}}, "detail": "invalid"},
	}
	for i, in := range cases {
		if _, err := engine.InvestigateChange(ctx, in); err == nil {
			t.Fatalf("case %d accepted malformed batch: %#v", i, in)
		}
	}
}
