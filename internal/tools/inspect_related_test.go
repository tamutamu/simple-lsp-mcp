package tools

import (
	"context"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"testing"
)

func TestInspectRelationsRejectInvalidRequests(t *testing.T) {
	cases := []map[string]any{
		{"related": "references"},
		{"related": []any{}},
		{"related": []any{"references", "references"}},
		{"related": []any{"not_lsp"}},
		{"related": []any{"references"}, "relation_limit": 0},
		{"related": []any{"references"}, "relation_limit": -1},
		{"related": []any{"references"}, "relation_limit": 21},
		{"related": []any{"references"}, "targets": []any{map[string]any{"path": "x.go", "symbol_path": "Fn"}}},
		{"related": []any{"references", "declaration", "implementations", "type_definition", "incoming_calls", "outgoing_calls", "subtypes"}},
	}
	var engine Engine
	for i, in := range cases {
		if _, err := engine.InspectSymbol(context.Background(), in); err == nil {
			t.Fatalf("case %d unexpectedly accepted: %#v", i, in)
		}
	}
	names, limit, err := parseInspectRelations(map[string]any{"related": []any{"references", "incoming_calls"}})
	if err != nil || limit != 8 || len(names) != 2 {
		t.Fatalf("valid related selection: %v / %d / %#v", err, limit, names)
	}
	names, limit, err = parseInspectRelations(map[string]any{})
	if err != nil || len(names) != 0 || limit != 8 {
		t.Fatalf("default must not request hidden LSP operations: %v / %d / %#v", err, limit, names)
	}
}

func TestRelatedReferencesKeepSameLineOccurrences(t *testing.T) {
	locs := []core.Location{
		{Path: "b.go", Range: core.Range{Start: core.Position{Line: 20, Column: 9}}},
		{Path: "a.go", Range: core.Range{Start: core.Position{Line: 5, Column: 6}}},
		{Path: "a.go", Range: core.Range{Start: core.Position{Line: 5, Column: 10}}},
	}
	grouped := groupRelatedReferences(locs)
	if len(grouped) != 2 || grouped[0]["path"] != "a.go" || grouped[1]["path"] != "b.go" || grouped[0]["count"] != 2 {
		t.Fatalf("incorrect reference grouping: %#v", grouped)
	}
	lines := grouped[0]["lines"].([]int)
	if len(lines) != 2 || lines[0] != 5 || lines[1] != 5 {
		t.Fatalf("same-line references lost: %#v", grouped)
	}
	compact := compactRelatedLocations(locs)
	if compact[0]["path"] != "a.go" || compact[0]["column"] != 6 || compact[1]["column"] != 10 {
		t.Fatalf("locations order: %#v", compact)
	}
}

func TestRelatedCallsRetainLSPIdentity(t *testing.T) {
	got := compactRelatedItems([]any{
		map[string]any{"symbol_id": "id-1", "name": "Call", "path": "main.go", "kind": "function",
			"selection_range": core.Range{Start: core.Position{Line: 8, Column: 5}}, "from_ranges": []any{1, 2, 3}},
	})
	if len(got) != 1 || got[0]["symbol_id"] != "id-1" || got[0]["line"] != 8 {
		t.Fatalf("exact identity lost: %#v", got)
	}
	if _, ok := got[0]["from_ranges"]; ok {
		t.Fatalf("large unused payload: %#v", got)
	}
}

func TestSharedWarmupOnlyForMultipleProjectRelationships(t *testing.T) {
	if inspectWarmRelationCount([]string{"references"}) != 1 ||
		inspectWarmRelationCount([]string{"references", "declaration"}) != 1 ||
		inspectWarmRelationCount([]string{"references", "incoming_calls"}) != 2 {
		t.Fatal("related context must warm only when multiple relations need it")
	}
	if inspectNeedsWarm("outgoing_calls") || inspectNeedsWarm("type_definition") ||
		!inspectNeedsPrime([]string{"implementations", "incoming_calls"}) ||
		inspectNeedsPrime([]string{"incoming_calls", "subtypes"}) {
		t.Fatal("incorrect LSP project warm-up dependencies")
	}
}

func TestRelatedLSPInternalOperationsAreMappedCorrectly(t *testing.T) {
	for related, internal := range map[string]string{
		"references":      "find_references",
		"implementations": "find_implementations",
		"incoming_calls":  "get_incoming_calls",
		"outgoing_calls":  "get_outgoing_calls",
		"supertypes":      "get_supertypes",
		"subtypes":        "get_subtypes",
		"type_definition": "get_type_definition",
		"declaration":     "get_declaration",
	} {
		if got := inspectInternalToolName(related); got != internal {
			t.Fatalf("%s: got %s, want %s", related, got, internal)
		}
	}
}