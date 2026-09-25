package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
			server := config.Server{Command: tc.binary, Args: tc.args, Directory: "."}
			// The CI fixture is outside node_modules; use its installed TypeScript.
			if tc.name == "typescript" && os.Getenv("SIMPLE_LSP_TSSERVER_PATH") != "" {
				server.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": os.Getenv("SIMPLE_LSP_TSSERVER_PATH")}}
			}
			cfg := config.Runtime{Workspace: root, RequestTimeout: 30 * time.Second, MaxResults: 100, Servers: map[string][]config.Server{
				tc.profile: {server},
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
			listed, err := engine.ListWorkspaceSymbols(ctx, map[string]any{})
			if err != nil {
				t.Fatalf("real workspace enumeration: %v", err)
			}
			list, ok := listed["symbols"].([]core.SymbolSummary)
			if !ok || !listed["meta"].(core.Meta).Complete {
				t.Fatalf("unexpected workspace enumeration: %#v", listed)
			}
			foundAdd := false
			for _, item := range list {
				if item.Name == "Add" && item.Path == tc.filename && item.SymbolPath != "" {
					foundAdd = true
				}
			}
			if !foundAdd {
				t.Fatalf("workspace enumeration missed Add: %#v", listed)
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

// TestRealGoSemanticSlice validates semantic context content rather than only
// successful LSP initialization. This is a deterministic offline quality gate.
func TestRealGoSemanticSlice(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" && os.Getenv("SIMPLE_LSP_REAL_LSP") != "go" {
		t.Skip("requires real gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/semantics\n\ngo 1.26.0\n",
		"service.go":      "package semantics\nfunc Add(a, b int) int { return a+b }\nfunc Twice(v int) int { return Add(v,v) }\n",
		"service_test.go": "package semantics\nimport \"testing\"\nfunc TestTwice(t *testing.T) { if Twice(2) != 4 { t.Fatal(\"bad\") } }\n",
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{Workspace: root, MaxResults: 100, RequestTimeout: 30 * time.Second, Servers: map[string][]config.Server{
		"go": {{Command: "gopls", Directory: "."}},
	}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := e.DocumentSymbols(ctx, map[string]any{"path": "service_test.go"}); err != nil {
		t.Fatalf("test file document symbols: %v", err)
	}
	slice, err := e.SemanticSlice(ctx, map[string]any{"path": "service.go", "symbol_path": "Twice", "max_bytes": 24576, "depth": 2})
	if err != nil {
		t.Fatalf("semantic slice: %v", err)
	}
	rootSymbol, ok := slice["root"].(map[string]any)
	if !ok || rootSymbol["name"] != "Twice" || !strings.Contains(rootSymbol["source"].(string), "Add(v,v)") {
		t.Fatalf("root source was not recovered: %#v", slice)
	}
	deps, ok := slice["dependencies"].([]any)
	if !ok {
		t.Fatalf("dependencies: %#v", slice)
	}
	foundAdd := false
	for _, raw := range deps {
		dep, ok := raw.(map[string]any)
		if ok && dep["name"] == "Add" && strings.Contains(dep["source"].(string), "a+b") {
			foundAdd = true
		}
	}
	if !foundAdd {
		t.Fatalf("semantic slice omitted callee source: %#v", slice)
	}
	if got := encodedSize(slice); got > 24576 {
		t.Fatalf("JSON response size %d exceeded max_bytes", got)
	}
	for _, raw := range slice["related_tests"].([]any) {
		candidate := raw.(map[string]any)
		if candidate["path"] == "service_test.go" {
			return
		}
	}
	t.Fatalf("semantic slice omitted reference-backed test file: %#v", slice)
}

// TestRealWorkspaceEnumeration verifies that listing works without a name query,
// includes multiple module roots, and resumes after a symbol-level page break.
func TestRealWorkspaceEnumeration(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" && os.Getenv("SIMPLE_LSP_REAL_LSP") != "go" {
		t.Skip("requires real gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, source := range map[string]string{
		"a": "package a\nfunc Alpha() {}\nfunc Middle() {}\n",
		"b": "package b\nfunc Omega() {}\n",
	} {
		dir := filepath.Join(root, "apps", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/"+name+"\n\ngo 1.26.0\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".go"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Dependency trees are not part of the source inventory.
	ignored := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(ignored, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "ignore.go"), []byte("package ignored\nfunc MustNotAppear() {}\n"), 0600); err != nil {
		t.Fatal(err)
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
	seen := map[string]string{}
	cursor := ""
	for iteration := 0; iteration < 12; iteration++ {
		in := map[string]any{"limit": 1}
		if cursor != "" {
			in["cursor"] = cursor
		}
		page, err := e.ListWorkspaceSymbols(ctx, in)
		if err != nil {
			t.Fatalf("list page %d: %v", iteration, err)
		}
		symbols, ok := page["symbols"].([]core.SymbolSummary)
		if !ok || len(symbols) > 1 {
			t.Fatalf("invalid page: %#v", page)
		}
		for _, symbol := range symbols {
			if symbol.Name == "MustNotAppear" || symbol.SymbolPath == "" || symbol.SymbolID == "" {
				t.Fatalf("incorrect symbol: %#v", symbol)
			}
			if _, duplicate := seen[symbol.Name]; duplicate {
				t.Fatalf("repeated symbol on subsequent page: %s", symbol.Name)
			}
			seen[symbol.Name] = symbol.Path
			if _, err := e.GetSymbol(ctx, map[string]any{"symbol_id": symbol.SymbolID}); err != nil {
				t.Fatalf("listed symbol cannot be retrieved: %v", err)
			}
		}
		next, more := page["next_cursor"].(string)
		meta := page["meta"].(core.Meta)
		if !more {
			if !meta.Complete || meta.Truncated {
				t.Fatalf("final page meta=%#v", meta)
			}
			break
		}
		if meta.Complete || !meta.Truncated {
			t.Fatalf("continuation page meta=%#v", meta)
		}
		cursor = next
		if iteration == 11 {
			t.Fatal("listing never terminated")
		}
	}
	if seen["Alpha"] != "apps/a/a.go" || seen["Middle"] != "apps/a/a.go" || seen["Omega"] != "apps/b/b.go" {
		t.Fatalf("workspace enumeration missed symbols: %#v", seen)
	}
	first, err := e.ListWorkspaceSymbols(ctx, map[string]any{"limit": 1, "language": "go"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps/a/a.go"), []byte("package a\nfunc Alpha() {}\nfunc Middle() {}\nfunc Later() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = e.ListWorkspaceSymbols(ctx, map[string]any{"limit": 1, "language": "go", "cursor": first["next_cursor"]})
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.InvalidArgument {
		t.Fatalf("stale in-file cursor accepted: %v", err)
	}
}

// TestRealReactFastAPIMonorepoExample is the executable proof behind
// examples/react-fastapi-monorepo. It intentionally uses two real language
// servers from one Engine to verify mixed-language routing rather than testing
// each LSP in isolation.
func TestRealReactFastAPIMonorepoExample(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("requires real TypeScript Language Server and Pyright")
	}
	for _, binary := range []string{"typescript-language-server", "pyright-langserver"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("%s not installed: %v", binary, err)
		}
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", "examples", "react-fastapi-monorepo"))
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: 30 * time.Second, MaxResults: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers["typescript-javascript"]) != 1 || len(cfg.Servers["python"]) != 1 {
		t.Fatalf("example did not load both language profiles: %#v", cfg.Servers)
	}
	tsServer := cfg.Servers["typescript-javascript"][0]
	if len(tsServer.Settings) == 0 || len(tsServer.InitializationOptions) == 0 {
		t.Fatalf("TypeScript example lost settings or initialization_options: %#v", tsServer)
	}
	pyServer := cfg.Servers["python"][0]
	if pyServer.Env["PYTHONPATH"] != "." || len(pyServer.Settings) == 0 {
		t.Fatalf("Python example lost env or settings: %#v", pyServer)
	}
	// CI exposes the exact TypeScript installation path for deterministic real-LSP tests.
	// Preserve the example's own initialization options while adding that runtime-only hint.
	if path := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); path != "" {
		if tsServer.InitializationOptions == nil {
			tsServer.InitializationOptions = map[string]any{}
		}
		tsServer.InitializationOptions["tsserver"] = map[string]any{"path": path}
		cfg.Servers["typescript-javascript"][0] = tsServer
	}

	e := New(ws, cfg)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	tsPath := "apps/web/src/components/UserList.tsx"
	tsFound, err := e.FindSymbol(ctx, map[string]any{"path": tsPath, "symbol_path": "loadUsers"})
	if err != nil {
		t.Fatalf("TypeScript symbol lookup: %v", err)
	}
	tsSymbol, ok := tsFound["symbol"].(map[string]any)
	if !ok || tsSymbol["language"] != "typescriptreact" || !strings.Contains(tsSymbol["source"].(string), "fetchUsers(apiBase)") {
		t.Fatalf("wrong TypeScript symbol: %#v", tsFound)
	}

	pyPath := "apps/api/app/routes/users.py"
	pyFound, err := e.FindSymbol(ctx, map[string]any{"path": pyPath, "symbol_path": "list_users"})
	if err != nil {
		t.Fatalf("Python symbol lookup: %v", err)
	}
	pySymbol, ok := pyFound["symbol"].(map[string]any)
	if !ok || pySymbol["language"] != "python" || !strings.Contains(pySymbol["source"].(string), "get_users()") {
		t.Fatalf("wrong Python symbol: %#v", pyFound)
	}

	listed, err := e.ListWorkspaceSymbols(ctx, map[string]any{"limit": 200})
	if err != nil {
		t.Fatalf("mixed-language workspace enumeration: %v", err)
	}
	symbols, ok := listed["symbols"].([]core.SymbolSummary)
	if !ok || !listed["meta"].(core.Meta).Complete {
		t.Fatalf("unexpected mixed-language workspace page: %#v", listed)
	}
	foundTS, foundPython := false, false
	languages := map[string]bool{}
	for _, symbol := range symbols {
		languages[symbol.Language] = true
		if symbol.Name == "loadUsers" && symbol.Path == tsPath {
			foundTS = true
		}
		if symbol.Name == "list_users" && symbol.Path == pyPath {
			foundPython = true
		}
	}
	if !foundTS || !foundPython || !languages["typescriptreact"] || !languages["python"] {
		t.Fatalf("one workspace listing did not cover both projects: languages=%v symbols=%#v", languages, symbols)
	}

	for _, tc := range []struct {
		name, path, symbol, dependency string
	}{
		{"typescript", tsPath, "loadUsers", "fetchUsers"},
		{"python", pyPath, "list_users", "get_users"},
	} {
		t.Run(tc.name+"_semantic_slice", func(t *testing.T) {
			slice, err := e.SemanticSlice(ctx, map[string]any{"path": tc.path, "symbol_path": tc.symbol, "depth": 1, "max_bytes": 24576})
			if err != nil {
				t.Fatalf("semantic slice: %v", err)
			}
			rootSymbol, ok := slice["root"].(map[string]any)
			if !ok || rootSymbol["name"] != tc.symbol {
				t.Fatalf("wrong semantic root: %#v", slice)
			}
			deps, _ := slice["dependencies"].([]any)
			for _, raw := range deps {
				dep, _ := raw.(map[string]any)
				if dep["name"] == tc.dependency && strings.TrimSpace(dep["source"].(string)) != "" {
					return
				}
			}
			t.Fatalf("semantic slice did not follow %s -> %s across files: %#v", tc.symbol, tc.dependency, slice)
		})
	}
}
