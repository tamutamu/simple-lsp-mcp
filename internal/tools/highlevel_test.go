package tools

import (
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestIncludeSectionDefaultsToEverySection(t *testing.T) {
	if !includeSection(map[string]any{}, "source") {
		t.Fatal("includeSection should default to true when include is omitted")
	}
}

func TestIncludeSectionRestrictsToNamedSections(t *testing.T) {
	in := map[string]any{"include": []any{"source", "references"}}
	if !includeSection(in, "source") {
		t.Fatal("source should be included")
	}
	if includeSection(in, "incoming_calls") {
		t.Fatal("incoming_calls should not be included")
	}
}

func TestGroupReferencesCollapsesLinesPerFile(t *testing.T) {
	locs := []core.Location{
		{Path: "a.go", Range: core.Range{Start: core.Position{Line: 3}}},
		{Path: "b.go", Range: core.Range{Start: core.Position{Line: 9}}},
		{Path: "a.go", Range: core.Range{Start: core.Position{Line: 7}}},
	}
	got := groupReferences(locs)
	if len(got) != 2 {
		t.Fatalf("groupReferences = %#v, want 2 files", got)
	}
	first := got[0].(map[string]any)
	if first["path"] != "a.go" || first["count"] != 2 {
		t.Fatalf("first = %#v", first)
	}
	lines, ok := first["lines"].([]int)
	if !ok || len(lines) != 2 || lines[0] != 3 || lines[1] != 7 {
		t.Fatalf("lines = %#v", first["lines"])
	}
}

func TestCompactCallProjectsWhitelistedKeysOnly(t *testing.T) {
	call := map[string]any{
		"symbol_id": "sym_a", "name": "f", "kind": "function", "path": "a.go",
		"range": core.Range{Start: core.Position{Line: 5}}, "selection_range": core.Range{},
		"from_ranges": []core.Range{{}},
	}
	got := compactCall(call)
	if got["symbol_id"] != "sym_a" || got["name"] != "f" || got["kind"] != "function" || got["path"] != "a.go" {
		t.Fatalf("compactCall = %#v", got)
	}
	if got["line"] != 5 {
		t.Fatalf("line = %#v, want 5", got["line"])
	}
	if _, ok := got["from_ranges"]; ok {
		t.Fatalf("compactCall should drop from_ranges: %#v", got)
	}
	if _, ok := got["selection_range"]; ok {
		t.Fatalf("compactCall should drop selection_range: %#v", got)
	}
}

func TestWithLimitCopiesAndAddsLimit(t *testing.T) {
	subIn := map[string]any{"symbol_id": "sym_a", "language": "go"}
	got := withLimit(subIn, 7)
	if got["limit"] != 7 || got["symbol_id"] != "sym_a" || got["language"] != "go" {
		t.Fatalf("withLimit = %#v", got)
	}
	if _, ok := subIn["limit"]; ok {
		t.Fatal("withLimit must not mutate its input")
	}
}
