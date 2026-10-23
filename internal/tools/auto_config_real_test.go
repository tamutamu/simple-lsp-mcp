package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// TestRealAutoDetectedLanguageServers verifies that zero-config startup
// genuinely routes Go, Python and TypeScript to their installed LSPs without
// generating YAML or downloading packages. The real-LSP CI suite runs it.
func TestRealAutoDetectedLanguageServers(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("real LSP integration opt-in")
	}
	for _, bin := range []string{"gopls", "pyright-langserver", "typescript-language-server"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("missing %s: %v", bin, err)
		}
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                 "module example.com/zero-config\n\ngo 1.26.0\n",
		"hello.go":               "package proof\nfunc Hello() string { return \"hello\" }\n",
		"backend/pyproject.toml": "[project]\nname='zero-config'\nversion='0.0.1'\n",
		"backend/app.py":         "def greet(name: str) -> str:\n    return name\n",
		"frontend/package.json":  "{\"name\":\"zero-config\",\"private\":true}",
		"frontend/tsconfig.json": "{\"compilerOptions\":{\"strict\":true},\"include\":[\"src/**/*.ts\"]}",
		"frontend/src/app.ts":    "export function greet(name: string) { return name; }\n",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := config.Load(config.Runtime{Workspace: root, RequestTimeout: 30 * time.Second, MaxResults: 100})
	if err != nil {
		t.Fatal(err)
	}
	for profile, directory := range map[string]string{"go": ".", "python": "backend", "typescript-javascript": "frontend"} {
		entries := runtime.Servers[profile]
		if len(entries) != 1 || entries[0].Directory != directory || entries[0].Managed {
			t.Fatalf("%s was not correctly autodetected: %#v", profile, entries)
		}
	}
	if tsServer := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); tsServer != "" {
		runtime.Servers["typescript-javascript"][0].InitializationOptions = map[string]any{
			"tsserver": map[string]any{"path": tsServer},
		}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, runtime)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, path := range []string{"hello.go", "backend/app.py", "frontend/src/app.ts"} {
		got, err := engine.DocumentSymbols(ctx, map[string]any{"path": path})
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(got) == 0 {
			t.Errorf("%s returned an empty response", path)
		}
	}
	for _, name := range []string{config.ConfigFile, config.ConfigFileAlt} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("zero-config startup generated %s: %v", name, err)
		}
	}
}
