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
		"go.mod":          "module example.com/impact-fallback\n\ngo 1.26.0\n",
		"config.go":       "package fixture\nconst TaxRate = 12\ntype Pricing struct { Discount int }\n",
		"invoice.go":      "package fixture\nfunc Invoice(v int) int { return v * TaxRate / 100 }\n",
		"order.go":        "package fixture\nfunc Order(v int) int { return v + TaxRate }\nfunc Adjustment(p Pricing) int {return p.Discount}\n",
		"handler.go":      "package fixture\nfunc HandleInvoice(v int) int { return Invoice(v) }\n",
		"taxrate_test.go": "package fixture\nimport \"testing\"\nfunc TestTaxRate(t *testing.T) { if TaxRate == 0 {t.Fatal(\"invalid rate\")} }\n",
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
	if !ok || len(refs) != 3 {
		t.Fatalf("want 3 references including a test, got %#v", direct)
	}
	paths := map[string]bool{}
	for _, ref := range refs {
		if l, ok := ref.(core.Location); ok {
			paths[l.Path] = true
		}
	}
	if !paths["invoice.go"] || !paths["order.go"] || !paths["taxrate_test.go"] {
		t.Fatalf("wrong reference paths: %#v", paths)
	}
	if got["analysis_mode"] != "reference_usage_graph" {
		t.Fatalf("expected transitive LSP usage graph, got %#v", got["analysis_mode"])
	}
	dependents, ok := direct["dependent_symbols"].([]any)
	if !ok || len(dependents) != 3 {
		t.Fatalf("expected two application functions and a test function, got %#v", direct)
	}
	names := map[string]bool{}
	for _, raw := range dependents {
		entry := raw.(map[string]any)
		names[entry["name"].(string)] = true
	}
	if !names["Invoice"] || !names["Order"] || !names["TestTaxRate"] {
		t.Fatalf("unexpected dependent symbols: %#v", dependents)
	}
	var testCandidate bool
	for _, raw := range dependents {
		entry := raw.(map[string]any)
		if entry["name"] == "TestTaxRate" {
			testCandidate = entry["test_candidate"] == true
		}
	}
	if !testCandidate {
		t.Fatalf("missed a test that directly refers to TaxRate: %#v", dependents)
	}
	// The returned dependent symbol ID is actionable: the agent can retrieve
	// exactly that declaration's source without another search by name.
	for _, raw := range dependents {
		entry := raw.(map[string]any)
		if entry["name"] != "Invoice" {
			continue
		}
		got, err := e.GetSymbol(ctx, map[string]any{"symbol_id": entry["symbol_id"], "include_source": true})
		if err != nil {
			t.Fatalf("dependent symbol handle cannot be read: %v", err)
		}
		source := got["symbol"].(map[string]any)["source"].(string)
		if !strings.Contains(source, "TaxRate") {
			t.Fatalf("wrong owner source: %q", source)
		}
	}
	indirect := got["indirect"].(map[string]any)["callers"].([]any)
	if len(indirect) != 1 || indirect[0].(map[string]any)["name"] != "HandleInvoice" {
		t.Fatalf("missing caller through Invoice: %#v", indirect)
	}
	if _, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_id": sym["symbol_id"], "include_references": false}); err == nil {
		t.Fatal("explicit include_references=false was silently overridden in reference-only mode")
	}
	affected, ok := got["affected_files"].([]any)
	if !ok || len(affected) != 5 {
		t.Fatalf("expected definition, two sources, one test and indirect caller, got %#v", affected)
	}
	// The exact same symbol should work from an arbitrary position inside its name.
	byPosition, err := e.ImpactAnalysis(ctx, map[string]any{"path": "config.go", "line": 2, "column": 10, "depth": 2})
	if err != nil {
		t.Fatalf("declaration-position reference analysis failed: %v", err)
	}
	if byPosition["analysis_mode"] != "reference_usage_graph" {
		t.Fatalf("position selected wrong analysis mode: %#v", byPosition)
	}
	// And the human-readable symbol_path route must not require a pre-existing symbol_id.
	byPath, err := e.ImpactAnalysis(ctx, map[string]any{"symbol_path": "TaxRate", "path": "config.go", "depth": 2})
	if err != nil || byPath["analysis_mode"] != "reference_usage_graph" {
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
	if meta.Depth != 2 || meta.RequestedDepth != 2 {
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
			"handler.py":         "from invoice import invoice\ndef handle_invoice(v: int) -> int: return invoice(v)\n",
		}},
		{name: "typescript", profile: "typescript-javascript", bin: "typescript-language-server", argsSymbol: "taxRate", defFile: "rates.ts", args: []string{"--stdio"}, files: map[string]string{
			"tsconfig.json": "{\"compilerOptions\":{\"strict\":true,\"moduleResolution\":\"bundler\"},\"include\":[\"*.ts\"]}",
			"rates.ts":      "export const taxRate = 12;\n",
			"invoice.ts":    "import { taxRate } from './rates';\nexport const invoice = (n:number) => n*taxRate/100;\n",
			"order.ts":      "import { taxRate } from './rates';\nexport const order = (n:number) => n+taxRate;\n",
			"handler.ts":    "import { invoice } from './invoice';\nexport const handleInvoice = (n:number) => invoice(n);\n",
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
			// For data symbols, identify the *functions* using them, not merely
			// the file locations returned by the raw references primitive.
			direct := got["direct"].(map[string]any)
			owners, ok := direct["dependent_symbols"].([]any)
			if !ok || len(owners) < 2 {
				t.Fatalf("missing enclosing usage symbols: %#v", direct)
			}
			names := map[string]bool{}
			for _, raw := range owners {
				entry := raw.(map[string]any)
				names[entry["name"].(string)] = true
			}
			if !names["invoice"] || !names["order"] {
				t.Fatalf("incorrect dependent symbols: %#v", owners)
			}
			if got["analysis_mode"] != "reference_usage_graph" {
				t.Fatalf("wanted two-hop usage graph for %s, got %#v, meta=%+v", tc.name, got["analysis_mode"], got["meta"])
			}
			callerName := "handle_invoice"
			if tc.name == "typescript" {
				callerName = "handleInvoice"
			}
			callers := got["indirect"].(map[string]any)["callers"].([]any)
			foundCaller := false
			for _, raw := range callers {
				caller := raw.(map[string]any)
				via, _ := caller["via"].(map[string]any)
				if caller["name"] == callerName && via["name"] == "invoice" {
					foundCaller = true
				}
			}
			if !foundCaller {
				t.Fatalf("missing indirect caller %s -> invoice: %#v", callerName, callers)
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
