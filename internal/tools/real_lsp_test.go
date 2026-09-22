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

// TestRealLanguageServers uses actual LSP subprocesses, not mocked protocol
// responses. CI enables it after installing gopls, pyright, and tsserver.
// Local developers can opt in using SIMPLE_LSP_REAL_LSP=1.
func TestRealLanguageServers(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") == "" {
		t.Skip("set SIMPLE_LSP_REAL_LSP=1 (all) or go (Go only)")
	}
	cases := []struct {
		name, profile, binary, source, filename, original, changed, setup string
		args                                                              []string
	}{
		{"go", "go", "gopls", "package fixture\nfunc Add(a int, b int) int { return a + b }\nfunc Twice(v int) int { return Add(v, v) }\n", "fixture.go", "a + b", "a - b", "go.mod", nil},
		{"typescript", "typescript-javascript", "typescript-language-server", "export function Add(a: number, b: number) { return a + b; }\nexport function Twice(v: number) { return Add(v, v); }\n", "fixture.ts", "a + b", "a - b", "tsconfig.json", []string{"--stdio"}},
		{"python", "python", "pyright-langserver", "def Add(a: int, b: int) -> int:\n    return a + b\n\ndef Twice(v: int) -> int:\n    return Add(v, v)\n", "fixture.py", "a + b", "a - b", "pyrightconfig.json", []string{"--stdio"}},
	}
	for _, tc := range cases {
		if os.Getenv("SIMPLE_LSP_REAL_LSP") == "go" && tc.name != "go" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Fatalf("%s not installed: %v", tc.binary, err)
			}
			root := t.TempDir()
			setupContents := map[string]string{"go.mod": "module example.com/fixture\n\ngo 1.26.0\n", "tsconfig.json": "{\"compilerOptions\":{\"strict\":true},\"include\":[\"*.ts\"]}\n", "pyrightconfig.json": "{\"include\":[\".\"]}\n"}
			if err := os.WriteFile(filepath.Join(root, tc.setup), []byte(setupContents[tc.setup]), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.filename), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			ws, err := workspace.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Runtime{Workspace: root, RequestTimeout: 30 * time.Second, MaxResults: 100, Servers: map[string][]config.Server{
				tc.profile: {{Command: tc.binary, Args: tc.args, Directory: "."}},
			}}
			engine := New(ws, cfg)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				engine.Sessions.Shutdown(ctx)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if _, err := engine.DocumentSymbols(ctx, map[string]any{"path": tc.filename}); err != nil {
				t.Fatalf("real documentSymbol: %v", err)
			}
			hit, err := engine.FindSymbol(ctx, map[string]any{"path": tc.filename, "symbol_path": "Add"})
			if err != nil {
				t.Fatalf("real find_symbol: %v", err)
			}
			symbol, ok := hit["symbol"].(map[string]any)
			if !ok {
				t.Fatalf("missing unique symbol: %#v", hit)
			}
			id, _ := symbol["symbol_id"].(string)
			src, _ := symbol["source"].(string)
			if id == "" || !strings.Contains(src, tc.original) {
				t.Fatalf("symbol source = %#v", symbol)
			}
			modified := strings.Replace(tc.source, tc.original, tc.changed, 1)
			if err := os.WriteFile(filepath.Join(root, tc.filename), []byte(modified), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.GetSymbol(ctx, map[string]any{"symbol_id": id}); err == nil {
				t.Fatal("stale symbol accepted after source change")
			}
			updated, err := engine.FindSymbol(ctx, map[string]any{"path": tc.filename, "symbol_path": "Add"})
			if err != nil {
				t.Fatalf("real symbol lookup after edit: %v", err)
			}
			updatedSymbol, ok := updated["symbol"].(map[string]any)
			if !ok || !strings.Contains(updatedSymbol["source"].(string), tc.changed) {
				t.Fatalf("updated symbol = %#v", updated)
			}
		})
	}
}

func TestRealGoMonorepoSearchesEveryServer(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" && os.Getenv("SIMPLE_LSP_REAL_LSP") != "go" {
		t.Skip("requires real gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, fn := range map[string]string{"a": "AlphaA", "b": "AlphaB"} {
		dir := filepath.Join(root, "apps", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/"+name+"\n\ngo 1.26.0\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".go"), []byte("package "+name+"\nfunc "+fn+"() {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{Workspace: root, MaxResults: 100, RequestTimeout: 30 * time.Second, Servers: map[string][]config.Server{
		"go": {{Command: "gopls", Directory: "apps/a"}, {Command: "gopls", Directory: "apps/b"}},
	}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var found map[string]any
	for ctx.Err() == nil {
		found, err = e.FindSymbol(ctx, map[string]any{"symbol_path": "AlphaB", "language": "go"})
		if err == nil {
			break
		}
		// gopls may need time to finish indexing both workspaces.
		if !strings.Contains(err.Error(), "SYMBOL_NOT_FOUND") {
			t.Fatalf("monorepo lookup: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("monorepo search did not index second workspace: %v", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
	symbol, ok := found["symbol"].(map[string]any)
	if !ok || symbol["path"] != "apps/b/b.go" {
		t.Fatalf("wrong monorepo symbol: %#v", found)
	}
	// The low-level workspace tool must aggregate every configured server too.
	workspaceResult, err := e.SearchSymbols(ctx, map[string]any{"language": "go", "query": "AlphaB"})
	if err != nil {
		t.Fatalf("monorepo workspace search: %v", err)
	}
	summaries, ok := workspaceResult["symbols"].([]core.SymbolSummary)
	if !ok {
		t.Fatalf("unexpected search result: %#v", workspaceResult)
	}
	foundB := false
	for _, summary := range summaries {
		if summary.Path == "apps/b/b.go" && summary.Name == "AlphaB" {
			foundB = true
		}
	}
	if !foundB {
		t.Fatalf("workspace search missed second server: %#v", workspaceResult)
	}
}
