package tools

import (
	"context"
	"fmt"
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

// BenchmarkRealTypeScriptReferenceWorkflow measures an actual LSP-backed
// find_references operation: symbol resolution, bounded candidate discovery,
// LSP synchronization, TypeScript semantic priming, and result decoding.
// The fixture deliberately contains many "grid" identifiers that must not
// match "id". Both base and optimized revisions run this exact benchmark.
func BenchmarkRealTypeScriptReferenceWorkflow(b *testing.B) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		b.Skip("requires installed TypeScript language server")
	}
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		b.Fatal(err)
	}
	root := b.TempDir()
	files := map[string]string{
		"tsconfig.json": `{"compilerOptions":{"strict":true,"target":"es2020","module":"commonjs"},"include":["*.ts"]}`,
		"api.ts": "export function id(n: number): number { return n + 1; }\n",
	}
	for i := 0; i < 8; i++ {
		files[fmt.Sprintf("client-%03d.ts", i)] =
			fmt.Sprintf("import { id } from './api'; export const use%d = id(%d);\n", i, i)
	}
	for i := 0; i < 100; i++ {
		files[fmt.Sprintf("unrelated-%03d.ts", i)] =
			strings.Repeat("// grid grid grid grid\n", 256)
	}
	for path, source := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(source), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	ws, err := workspace.Open(root)
	if err != nil { b.Fatal(err) }
	server := config.Server{Command: "typescript-language-server", Args: []string{"--stdio"}, Directory: "."}
	if tsserver := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); tsserver != "" {
		server.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": tsserver}}
	}
	engine := New(ws, config.Runtime{
		Workspace: root, RequestTimeout: 30*time.Second, MaxResults: 1000,
		Servers: map[string][]config.Server{"typescript-javascript": {server}},
	})
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	outline, err := engine.DocumentSymbols(ctx, map[string]any{"path": "api.ts"})
	if err != nil { b.Fatalf("LSP initialization/outline failed: %v", err) }
	var symbolID string
	for _, item := range outline["symbols"].([]any) {
		entry := item.(map[string]any)
		if entry["name"] == "id" {
			symbolID, _ = entry["symbol_id"].(string)
		}
	}
	if symbolID == "" { b.Fatalf("id not in real LSP outline: %#v", outline) }
	args := map[string]any{"symbol_id": symbolID, "relation_view": "locations", "include_declaration": true}
	find := func() {
		b.Helper()
		result, err := engine.Relationship(ctx, "find_references", "textDocument/references", "references", args)
		if err != nil { b.Fatalf("real find_references: %v", err) }
		locations, ok := result["locations"].([]core.Location)
		if !ok || len(locations) != 17 {
			b.Fatalf("real references count=%d, want 17 (declaration + 8 imports + 8 calls), result=%#v", len(locations), result)
		}
	}
	find() // Consume cold LSP startup and prove exact semantic acceptance.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ { find() }
}
