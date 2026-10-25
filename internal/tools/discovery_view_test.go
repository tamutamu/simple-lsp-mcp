package tools

import (
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestDiscoveryReturnsOnlyInformationNeededForIdentification(t *testing.T) {
	symbols := []core.SymbolSummary{
		{
			Name: "get_user_by_email", Kind: "function", SymbolID: "sym_123",
			SymbolPath: "crud/get_user_by_email", Path: "backend/app/crud.py",
			Language: "python",
			Range: core.Range{
				Start: core.Position{Line: 34, Column: 1},
				End:   core.Position{Line: 38, Column: 1},
			},
			SelectionRange: core.Range{
				Start: core.Position{Line: 35, Column: 5},
				End:   core.Position{Line: 35, Column: 22},
			},
		},
	}
	value := projectSymbols(symbols, "compact")
	compact, ok := value.([]map[string]any)
	if !ok || len(compact) != 1 {
		t.Fatalf("unexpected projection %#v", value)
	}
	got := compact[0]
	for _, key := range []string{"name", "kind", "path", "line", "symbol_id", "symbol_path"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing identification field %q", key)
		}
	}
	for _, key := range []string{"range", "selection_range", "language", "source", "code"} {
		if _, ok := got[key]; ok {
			t.Errorf("irrelevant field included by default %q", key)
		}
	}
	if got["line"] != 35 {
		t.Fatalf("incorrect one-based selection line: %#v", got)
	}

	full := projectSymbols(symbols, "full")
	typed, ok := full.([]core.SymbolSummary)
	if !ok || len(typed) != 1 || typed[0].Range.Start.Line != 34 {
		t.Fatalf("full detail lost original semantics: %#v", full)
	}
}

func TestDiscoveryProjectionPreservesPaginationAndCompleteness(t *testing.T) {
	page := map[string]any{
		"symbols":     []core.SymbolSummary{{Name: "Example", Kind: "class", SymbolID: "id", Path: "src/example.ts"}},
		"next_cursor": "unchanged",
		"meta":        core.Meta{Complete: false, Truncated: true},
	}
	got := projectSymbolPage(page, "compact")
	if got["next_cursor"] != "unchanged" || got["meta"].(core.Meta).Complete {
		t.Fatalf("paging metadata changed: %#v", got)
	}
	if _, ok := got["symbols"].([]map[string]any); !ok {
		t.Fatalf("page projection unexpectedly verbose: %#v", got)
	}
}

func TestDiscoveryDetailRejectsUnknownValues(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{"", "compact"}, {"compact", "compact"}, {"outline", "compact"}, {"full", "full"},
	} {
		got, err := symbolListDetail(map[string]any{"detail": tc.value})
		if err != nil || got != tc.want {
			t.Fatalf("detail %q -> %q err %v, want %q", tc.value, got, err, tc.want)
		}
	}
	if _, err := symbolListDetail(map[string]any{"detail": "source"}); err == nil {
		t.Fatal("full source is not an appropriate symbol-list response")
	}
}
