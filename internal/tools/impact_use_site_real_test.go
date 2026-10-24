package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// TestRealGoImpactFromUseSite targets a *use* of non-callable symbols in a
// different file than their declaration and expects the same analysis as
// targeting the declaration.
func TestRealGoImpactFromUseSite(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" && os.Getenv("SIMPLE_LSP_REAL_LSP") != "go" {
		t.Skip("requires real gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/impact-use\n\ngo 1.26.0\n",
		"config.go":  "package fixture\nconst TaxRate = 12\ntype Pricing struct { Discount int }\n",
		"invoice.go": "package fixture\nfunc Invoice(v int) int { return v * TaxRate / 100 }\n",
		"order.go":   "package fixture\nfunc Adjustment(p Pricing) int { return p.Discount }\n",
		"handler.go": "package fixture\nfunc HandleInvoice(v int) int { return Invoice(v) }\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{Workspace: root, RequestTimeout: 20 * time.Second, MaxResults: 100, Servers: map[string][]config.Server{"go": {{Command: "gopls", Args: []string{"serve"}, Directory: "."}}}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		e.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()

	column := func(file, needle string) int {
		line := strings.Split(files[file], "\n")[1]
		if file == "order.go" || file == "invoice.go" {
			return strings.Index(line, needle) + 1
		}
		t.Fatalf("unexpected file %s", file)
		return 0
	}

	cases := []struct {
		name, file, needle, symbolPath, kind string
		wantRefs                             int
		wantOwner                            string
	}{
		{"constant", "invoice.go", "TaxRate", "TaxRate", "constant", 1, "Invoice"},
		{"field", "order.go", "Discount", "Pricing/Discount", "field", 1, "Adjustment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decl, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_path": tc.symbolPath, "path": "config.go", "depth": 2})
			if err != nil {
				t.Fatalf("declaration target: %v", err)
			}
			use, err := e.ImpactAnalysis(ctx, map[string]any{"path": tc.file, "line": 2, "column": column(tc.file, tc.needle), "depth": 2})
			if err != nil {
				t.Fatalf("use-site target must resolve to the declaration: %v", err)
			}
			dr, ur := decl["root"].(map[string]any), use["root"].(map[string]any)
			for _, k := range []string{"symbol_path", "path", "name", "kind", "selection_range"} {
				if dr[k] != ur[k] {
					t.Fatalf("root %s differs: decl=%#v use=%#v", k, dr[k], ur[k])
				}
			}
			if ur["path"] != "config.go" {
				t.Fatalf("root must be the declaration, got %#v", ur)
			}
			if use["analysis_mode"] != decl["analysis_mode"] {
				t.Fatalf("analysis_mode: decl=%v use=%v", decl["analysis_mode"], use["analysis_mode"])
			}
			ud := use["direct"].(map[string]any)
			if refs := ud["references"].([]any); len(refs) != tc.wantRefs {
				t.Fatalf("want %d references, got %#v", tc.wantRefs, refs)
			}
			deps := ud["dependent_symbols"].([]any)
			if len(deps) != 1 || deps[0].(map[string]any)["name"] != tc.wantOwner {
				t.Fatalf("want owner %s, got %#v", tc.wantOwner, deps)
			}
			if tc.name == "constant" {
				ind := use["indirect"].(map[string]any)["callers"].([]any)
				if len(ind) != 1 || ind[0].(map[string]any)["name"] != "HandleInvoice" {
					t.Fatalf("want transitive caller HandleInvoice, got %#v", ind)
				}
			}
		})

	}

	// A callable use-site must continue to use Call Hierarchy rather than the
	// reference-only traversal selected for constants and fields.
	callLine := strings.Split(files["handler.go"], "\n")[1]
	callColumn := strings.LastIndex(callLine, "Invoice(") + 1
	call, err := e.ImpactAnalysis(ctx, map[string]any{"path": "handler.go", "line": 2, "column": callColumn + 2, "depth": 1})
	if err != nil {
		t.Fatalf("call use-site: %v", err)
	}
	callRoot := call["root"].(map[string]any)
	if callRoot["name"] != "Invoice" || callRoot["path"] != "invoice.go" || call["analysis_mode"] != "call_hierarchy" {
		t.Fatalf("callable use-site resolved incorrectly: root=%#v mode=%#v", callRoot, call["analysis_mode"])
	}
}
