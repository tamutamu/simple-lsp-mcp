package mcpserver

import (
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestReadableDiagnosticsPreservesExactCompilerMessageAndLocation(t *testing.T) {
	d := core.Diagnostic{
		Path:     "internal/tools/engine.go",
		Range:    core.Range{Start: core.Position{Line: 47, Column: 20}},
		Severity: "error", Code: "IncompatibleAssign", Source: "compiler",
		Message: "cannot use cfg.RequestTimeout (variable of int64 type time.Duration) as int value in struct literal",
	}
	data := map[string]any{"diagnostics": []core.Diagnostic{d}, "meta": core.Meta{Complete: true}}
	text, ok := renderReadableResult("get_diagnostics", map[string]any{"path": d.Path}, data)
	if !ok {
		t.Fatal("complete real diagnostic did not render")
	}
	for _, needle := range []string{"internal/tools/engine.go:47:20", "error", "IncompatibleAssign",
		"cannot use cfg.RequestTimeout", "int value in struct literal"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lost %s in %s", needle, text)
		}
	}
}

func TestReadableDiagnosticsDoesNotHideLSPUncertainty(t *testing.T) {
	d := core.Diagnostic{Path: "x.go", Range: core.Range{Start: core.Position{Line: 1, Column: 1}}, Severity: "error", Message: "wrong"}
	for _, data := range []map[string]any{
		{"diagnostics": []core.Diagnostic{d}, "meta": core.Meta{Complete: false}},
		{"diagnostics": []core.Diagnostic{d}, "meta": core.Meta{Complete: true, Truncated: true}},
		{"diagnostics": []core.Diagnostic{d}, "meta": core.Meta{Complete: true, Warnings: []string{"not ready"}}},
		{"diagnostics": []core.Diagnostic{d}, "meta": core.Meta{Complete: true, Servers: []string{"gopls"}}},
	} {
		if _, ok := renderReadableResult("get_diagnostics", map[string]any{"path": "x.go"}, data); ok {
			t.Fatalf("uncertain diagnostics rendered without warning: %#v", data)
		}
	}
	d.Locations = []core.Location{{Path: "x.go"}}
	if _, ok := renderReadableResult("get_diagnostics", map[string]any{"path": "x.go"}, map[string]any{
		"diagnostics": []core.Diagnostic{d}, "meta": core.Meta{Complete: true},
	}); ok {
		t.Fatal("related diagnostic locations would have been hidden")
	}
}
