package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestRealGoReferenceFilterKeepsProductionReferences(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") == "" {
		t.Skip("set SIMPLE_LSP_REAL_LSP=go to test against real gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":      "module example.com/referencefilter\n\ngo 1.26.0\n",
		"app.go":      "package referencefilter\nfunc Add(x int) int { return x + 1 }\nfunc Prod() int { return Add(3) }\n",
		"app_test.go": "package referencefilter\nimport \"testing\"\nfunc TestAdd(t *testing.T) { if Add(3) != 4 {t.Fatal(\"bad\")} }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{
		Workspace: root, RequestTimeout: 30 * time.Second, MaxResults: 100,
		Servers: map[string][]config.Server{"go": {{Command: "gopls", Directory: "."}}},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := engine.DocumentSymbols(ctx, map[string]any{"path": "app.go"}); err != nil {
		t.Fatal(err)
	}
	inputs := map[string]any{"path": "app.go", "symbol_path": "/Add", "relation_view": "lines", "include_declaration": false}
	unfiltered, err := engine.Relationship(ctx, "find_references", "textDocument/references", "references", inputs)
	if err != nil {
		t.Fatal(err)
	}
	inputs["exclude_test_files"] = true
	filtered, err := engine.Relationship(ctx, "find_references", "textDocument/references", "references", inputs)
	if err != nil {
		t.Fatal(err)
	}
	raw := unfiltered["locations"].([]core.Location)
	got := filtered["locations"].([]core.Location)
	for _, loc := range got {
		if loc.Path != "app.go" || loc.Range.Start.Line != 3 {
			t.Fatalf("filtered result included unrelated or test reference: %#v", got)
		}
	}
	if len(got) != 1 {
		t.Fatalf("production call missing: %#v", got)
	}
	filters, ok := filtered["filters"].(map[string]any)
	if !ok || filters["exclude_test_files"] != true || filters["excluded_count"] != len(raw)-len(got) {
		t.Fatalf("scope filter or excluded count incorrect: all=%#v filtered=%#v", unfiltered, filtered)
	}
	if !filtered["meta"].(core.Meta).Complete {
		t.Fatalf("filtered LSP result is not complete for selected scope: %#v", filtered)
	}
}
