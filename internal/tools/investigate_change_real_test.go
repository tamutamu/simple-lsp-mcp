package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// An actual TypeScript server resolves the two-hop caller chain and ignores
// unrelated identifiers. Completeness remains conservative when imports or
// the depth cutoff cannot be mapped to enclosing declarations.
func TestRealTypeScriptInvestigateChangeTwoHop(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("requires TypeScript language server")
	}
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tsconfig.json", `{"compilerOptions":{"strict":true,"target":"es2020","module":"commonjs"},"include":["*.ts"]}`)
	write("core.ts", "export function target(n: number): number { return n + 1; }\n")
	write("middle.ts", "import { target } from './core';\nexport function middle(n: number): number { return target(n); }\n")
	write("entry.ts", "import { middle } from './middle';\nexport function entry(n: number): number { return middle(n); }\n")
	for i := 0; i < 50; i++ {
		write(fmt.Sprintf("not-related-%03d.ts", i), "export const forgetTarget = 42;\n")
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	s := config.Server{Command: "typescript-language-server", Args: []string{"--stdio"}, Directory: "."}
	if tsserver := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); tsserver != "" {
		s.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": tsserver}}
	}
	engine := New(ws, config.Runtime{Workspace: root, RequestTimeout: 40 * time.Second,
		MaxResults: 200, Servers: map[string][]config.Server{"typescript-javascript": {s}}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	in := map[string]any{"path": "core.ts", "symbol_path": "target", "depth": 2, "max_nodes": 20}
	result, err := engine.InvestigateChange(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if result["affected_file_count"] != 2 || result["affected_declarations"] != 2 {
		t.Fatalf("incorrect transitive impact: %#v", result)
	}
	if _, ok := result["incomplete"].(bool); !ok {
		t.Fatalf("missing explicit incomplete bool: %#v", result)
	}
	// A deterministic presentation enables the agent to apply or report this
	// finding without a second search.
	paths := map[string]bool{}
	data, err := json.Marshal(result["affected_files"])
	if err != nil {
		t.Fatal(err)
	}
	var files []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &files); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		paths[file.Path] = true
	}
	if !paths["middle.ts"] || !paths["entry.ts"] || paths["core.ts"] {
		t.Fatalf("wrong transitive paths %#v", paths)
	}
	inspect, err := engine.InspectSymbol(ctx, map[string]any{"path": "core.ts", "symbol_path": "target", "view": "location"})
	if err != nil {
		t.Fatal(err)
	}
	selection := inspect["selection_range"].(core.Range)
	// Reproduce real Codex input: name and exact source coordinates are
	// both present. They corroborate one another rather than conflict.
	batch, err := engine.InvestigateChange(ctx, map[string]any{
		"targets": []any{
			map[string]any{"path": "core.ts", "symbol_path": "target",
				"line": selection.Start.Line, "column": selection.Start.Column},
			map[string]any{"path": "middle.ts", "symbol_path": "middle"},
		},
		"depth": 2, "max_nodes": 20, "detail": "union",
	})
	if err != nil {
		t.Fatalf("valid redundant coordinates rejected: %v", err)
	}
	if batch["targets_count"] != 2 {
		t.Fatalf("batch lost target: %#v", batch)
	}
	_, err = engine.InvestigateChange(ctx, map[string]any{
		"targets": []any{
			map[string]any{"path": "core.ts", "symbol_path": "target",
				"line": selection.Start.Line + 1, "column": selection.Start.Column},
			map[string]any{"path": "middle.ts", "symbol_path": "middle"},
		},
		"depth": 2, "max_nodes": 20, "detail": "union",
	})
	if err == nil {
		t.Fatal("stale/wrong coordinates were silently accepted")
	}
	graph, err := engine.AnalyzeImpact(ctx, map[string]any{"path": "core.ts", "line": selection.Start.Line,
		"column": selection.Start.Column, "depth": 2, "max_nodes": 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph["nodes"].([]impactNode))-1 != result["affected_declarations"] {
		t.Fatalf("high-level data drifted from base LSP graph: %#v", result)
	}
}
