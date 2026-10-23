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

// TestRealGoImpactForNonCallable demonstrates the benefit of combining
// references with the call-hierarchy API: constants are not call targets.
func TestRealGoImpactForNonCallable(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" && os.Getenv("SIMPLE_LSP_REAL_LSP") != "go" {
		t.Skip("requires real gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/impact-fallback\n\ngo 1.26.0\n",
		"config.go":  "package fixture\nconst TaxRate = 12\ntype Pricing struct { Discount int }\n",
		"invoice.go": "package fixture\nfunc Invoice(v int) int { return v * TaxRate / 100 }\n",
		"order.go":   "package fixture\nfunc Order(v int) int { return v + TaxRate }\nfunc Adjustment(p Pricing) int {return p.Discount}\n",
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
	symbol, err := e.FindSymbol(ctx, map[string]any{"symbol_path": "TaxRate", "path": "config.go"})
	if err != nil {
		t.Fatal(err)
	}
	sym := symbol["symbol"].(map[string]any)
	got, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_id": sym["symbol_id"], "depth": 2, "limit": 50})
	if err != nil {
		t.Fatalf("impact_analysis failed for valid LSP constant references: %v", err)
	}
	direct, ok := got["direct"].(map[string]any)
	if !ok {
		t.Fatalf("missing direct: %#v", got)
	}
	refs, ok := direct["references"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("want 2 reference locations across files, got %#v", direct)
	}
	paths := map[string]bool{}
	for _, ref := range refs {
		if l, ok := ref.(core.Location); ok {
			paths[l.Path] = true
		}
	}
	if !paths["invoice.go"] || !paths["order.go"] {
		t.Fatalf("wrong reference paths: %#v", paths)
	}
	if got["analysis_mode"] != "references_only" {
		t.Fatalf("expected reference-only mode, got %#v", got["analysis_mode"])
	}
	if _, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_id": sym["symbol_id"], "include_references": false}); err == nil {
		t.Fatal("explicit include_references=false was silently overridden in reference-only mode")
	}
	affected, ok := got["affected_files"].([]any)
	if !ok || len(affected) != 3 {
		t.Fatalf("expected definition and two references, got %#v", affected)
	}
	// The exact same symbol should work from an arbitrary position inside its name.
	byPosition, err := e.ImpactAnalysis(ctx, map[string]any{"path": "config.go", "line": 2, "column": 10, "depth": 2})
	if err != nil {
		t.Fatalf("declaration-position reference analysis failed: %v", err)
	}
	if byPosition["analysis_mode"] != "references_only" {
		t.Fatalf("position selected wrong analysis mode: %#v", byPosition)
	}
	// And the human-readable symbol_path route must not require a pre-existing symbol_id.
	byPath, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_path": "TaxRate", "path": "config.go", "depth": 2})
	if err != nil || byPath["analysis_mode"] != "references_only" {
		t.Fatalf("symbol_path reference analysis failed: %#v err=%v", byPath, err)
	}
	// Struct fields, not only constants, must work without Call Hierarchy.
	field, err := e.FindSymbol(ctx, map[string]any{"symbol_path": "Pricing/Discount", "path": "config.go"})
	if err != nil {
		t.Fatalf("field discovery: %v", err)
	}
	fieldSymbol := field["symbol"].(map[string]any)
	fieldImpact, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_id": fieldSymbol["symbol_id"]})
	if err != nil || fieldImpact["analysis_mode"] != "references_only" {
		t.Fatalf("field impact: %#v err=%v", fieldImpact, err)
	}
	meta := got["meta"].(impactMeta)
	if meta.Complete {
		t.Fatalf("reference-only results must not claim complete transitive impact: %+v", meta)
	}
	if meta.Depth != 0 || meta.RequestedDepth != 2 {
		t.Fatalf("wrong depth disclosure: %+v", meta)
	}
}

// TestRealImpactForDataSymbolsAcrossLanguages checks that a common non-callable
// code-change question works through real Pyright and TypeScript LSPs too.
func TestRealImpactForDataSymbolsAcrossLanguages(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("requires Python and TypeScript LSPs")
	}
	cases := []struct {
		name, profile, bin, argsSymbol, defFile string
		args                                    []string
		files                                   map[string]string
	}{
		{name: "python", profile: "python", bin: "pyright-langserver", argsSymbol: "TAX_RATE", defFile: "rates.py", args: []string{"--stdio"}, files: map[string]string{
			"pyrightconfig.json": "{\"include\":[\".\"]}",
			"rates.py":           "TAX_RATE: int = 12\n",
			"invoice.py":         "from rates import TAX_RATE\ndef invoice(v: int) -> int: return v * TAX_RATE // 100\n",
			"order.py":           "from rates import TAX_RATE\ndef order(v: int) -> int: return v + TAX_RATE\n",
		}},
		{name: "typescript", profile: "typescript-javascript", bin: "typescript-language-server", argsSymbol: "taxRate", defFile: "rates.ts", args: []string{"--stdio"}, files: map[string]string{
			"tsconfig.json": "{\"compilerOptions\":{\"strict\":true,\"moduleResolution\":\"bundler\"},\"include\":[\"*.ts\"]}",
			"rates.ts":      "export const taxRate = 12;\n",
			"invoice.ts":    "import { taxRate } from './rates';\nexport const invoice = (n:number) => n*taxRate/100;\n",
			"order.ts":      "import { taxRate } from './rates';\nexport const order = (n:number) => n+taxRate;\n",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.bin); err != nil {
				t.Skip(err)
			}
			root := t.TempDir()
			for name, src := range tc.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ws, err := workspace.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			srv := config.Server{Command: tc.bin, Args: tc.args, Directory: "."}
			if tc.name == "typescript" && os.Getenv("SIMPLE_LSP_TSSERVER_PATH") != "" {
				srv.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": os.Getenv("SIMPLE_LSP_TSSERVER_PATH")}}
			}
			e := New(ws, config.Runtime{Workspace: root, RequestTimeout: 25 * time.Second, MaxResults: 100, Servers: map[string][]config.Server{tc.profile: {srv}}})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				e.Sessions.Shutdown(ctx)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
			defer cancel()
			hit, err := e.FindSymbol(ctx, map[string]any{"path": tc.defFile, "symbol_path": tc.argsSymbol})
			if err != nil {
				t.Fatalf("find symbol: %v", err)
			}
			sym := hit["symbol"].(map[string]any)
			got, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_id": sym["symbol_id"], "depth": 2})
			if err != nil {
				t.Fatalf("impact_analysis for data symbol: %v", err)
			}
			if got["analysis_mode"] != "references_only" {
				t.Fatalf("unexpected mode for data symbol: %#v", got["analysis_mode"])
			}
			affected, ok := got["affected_files"].([]any)
			if !ok {
				t.Fatalf("missing affected files: %#v", got)
			}
			files := map[string]bool{}
			for _, raw := range affected {
				if m, ok := raw.(map[string]any); ok {
					files[m["path"].(string)] = true
				}
			}
			ext := ".py"
			if tc.name == "typescript" {
				ext = ".ts"
			}
			for _, name := range []string{tc.defFile, "invoice" + ext, "order" + ext} {
				if !files[name] {
					t.Fatalf("missing %s among affected files: %#v", name, affected)
				}
			}
			if got["meta"].(impactMeta).Complete {
				t.Fatal("reference-only transitive result falsely marked complete")
			}
		})
	}
}
