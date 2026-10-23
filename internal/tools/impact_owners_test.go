package tools

import (
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestInnermostEnclosingSymbolUsesLSPRanges(t *testing.T) {
	nodes := []symbolNode{
		{
			Name: "Outer", Kind: "function",
			Range: core.Range{Start: core.Position{Line: 1, Column: 1}, End: core.Position{Line: 12, Column: 1}},
			Children: []symbolNode{{
				Name: "Inner", Kind: "function", SymbolPath: "Outer/Inner",
				Range: core.Range{Start: core.Position{Line: 4, Column: 3}, End: core.Position{Line: 8, Column: 3}},
			}},
		},
		{Name: "Other", Kind: "function", Range: core.Range{Start: core.Position{Line: 13, Column: 1}, End: core.Position{Line: 15, Column: 1}}},
	}
	for _, test := range []struct {
		line, column int
		name         string
		found        bool
	}{
		{5, 4, "Inner", true},
		{10, 2, "Outer", true},
		{14, 2, "Other", true},
		{17, 1, "", false},
		{8, 3, "Outer", true}, // the inner symbol's end is exclusive
	} {
		got, ok := innermostEnclosingSymbol(nodes, core.Position{Line: test.line, Column: test.column})
		if ok != test.found || (ok && got.Name != test.name) {
			t.Errorf("%d:%d got=%v/%s, want=%v/%s", test.line, test.column, ok, got.Name, test.found, test.name)
		}
	}
}

func TestImpactTruncationDoesNotClaimCompleteness(t *testing.T) {
	b := newImpactBudget(t.Context())
	b.truncate("owner file limit reached")
	if !b.truncated || len(b.warnings) != 1 || b.warnings[0] != "owner file limit reached" {
		t.Fatalf("truncation not recorded: %+v", b)
	}
}

func TestImpactCallableOwnerKinds(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{"function", true}, {"method", true}, {"constructor", true},
		{"variable", true}, {"constant", true}, {"class", false},
		{"field", false}, {"struct", false}, {"interface", false},
	} {
		if got := impactCallableOwnerKind(tc.kind); got != tc.want {
			t.Errorf("%s got %v want %v", tc.kind, got, tc.want)
		}
	}
}
