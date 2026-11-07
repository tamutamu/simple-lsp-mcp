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

// A primed call-hierarchy request may be skipped only when the document store
// has not notified any new source to that server. Adding a new referencing file
// must invalidate the primed snapshot and return all semantic references.
func TestRealTypeScriptPrimeCacheOnNewReference(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("requires installed TypeScript language server")
	}
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tsconfig.json", `{"compilerOptions":{"strict":true,"target":"es2020"},"include":["*.ts"]}`)
	write("api.ts", "export function id(n: number): number { return n + 1; }\n")
	write("first.ts", "import { id } from './api'; export const first = id(1);\n")
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	server := config.Server{Command: "typescript-language-server", Args: []string{"--stdio"}, Directory: "."}
	if tsserver := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); tsserver != "" {
		server.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": tsserver}}
	}
	engine := New(ws, config.Runtime{Workspace: root, RequestTimeout: 30 * time.Second,
		MaxResults: 100, Servers: map[string][]config.Server{"typescript-javascript": {server}}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	outline, err := engine.DocumentSymbols(ctx, map[string]any{"path": "api.ts"})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, value := range outline["symbols"].([]any) {
		entry := value.(map[string]any)
		if entry["name"] == "id" {
			id, _ = entry["symbol_id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("target missing: %#v", outline)
	}
	find := func(want int) {
		t.Helper()
		result, err := engine.Relationship(ctx, "find_references", "textDocument/references",
			"references", map[string]any{"symbol_id": id, "include_declaration": true})
		if err != nil {
			t.Fatal(err)
		}
		locations, ok := result["locations"].([]core.Location)
		if !ok || len(locations) != want {
			t.Fatalf("reference count=%d, want %d: %#v", len(locations), want, result)
		}
	}
	find(3)
	find(3) // cache hit for unchanged set of opened files
	write("second.ts", "import { id } from './api'; export const second = id(2);\n")
	find(5) // candidate discovery + didOpen must invalidate the cache
	write("api.ts", "export function id(n: number): number { return n + 2; }\n")
	outline, err = engine.DocumentSymbols(ctx, map[string]any{"path": "api.ts"})
	if err != nil {
		t.Fatal(err)
	}
	// IDs from the previous document version are intentionally stale.
	id = ""
	for _, value := range outline["symbols"].([]any) {
		entry := value.(map[string]any)
		if entry["name"] == "id" {
			id, _ = entry["symbol_id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("target missing after edit: %#v", outline)
	}
	find(5) // didChange invalidates even when symbol spelling is unchanged
}
