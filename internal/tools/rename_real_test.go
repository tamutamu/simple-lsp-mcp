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

func TestRealRenameSymbol(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") == "" {
		t.Skip("set SIMPLE_LSP_REAL_LSP=1")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/rename\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "main.go")
	testPath := filepath.Join(root, "main_test.go")
	if err := os.WriteFile(mainPath, []byte("package rename\n\nfunc OldName() int { return 1 }\nfunc UseName() int { return OldName() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("package rename\n\nfunc ExampleOldName() { _ = OldName() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{
		RequestTimeout: 30 * time.Second,
		MaxResults:     100,
		Servers: map[string][]config.Server{
			"go": {{Command: "gopls", Directory: "."}},
		},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})

	input := map[string]any{
		"symbol_path": "OldName",
		"path":        "main.go",
		"language":    "go",
		"new_name":    "NewName",
	}
	preview, err := engine.RenameSymbol(context.Background(), input)
	if err != nil {
		t.Fatalf("rename preview: %v", err)
	}
	if preview["applied"] != false {
		t.Fatalf("preview applied unexpectedly: %#v", preview)
	}
	if preview["edit_count"].(int) < 3 || preview["file_count"].(int) < 2 {
		t.Fatalf("rename preview missed references: %#v", preview)
	}
	before, _ := os.ReadFile(mainPath)
	if !strings.Contains(string(before), "OldName") {
		t.Fatalf("preview modified main.go: %s", before)
	}

	input["apply"] = true
	applied, err := engine.RenameSymbol(context.Background(), input)
	if err != nil {
		t.Fatalf("rename apply: %v", err)
	}
	if applied["applied"] != true {
		t.Fatalf("rename not applied: %#v", applied)
	}
	for _, path := range []string{mainPath, testPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "OldName") || !strings.Contains(string(content), "NewName") {
			t.Fatalf("semantic rename incomplete in %s: %s", path, content)
		}
	}
}
