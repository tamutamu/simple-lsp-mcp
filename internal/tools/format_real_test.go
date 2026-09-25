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

func TestRealFormatDocument(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") == "" {
		t.Skip("set SIMPLE_LSP_REAL_LSP=1")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/format\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "format.go")
	original := "package format\n\nfunc  Add( a int,b int)int{return a+b}\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
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

	preview, err := engine.FormatDocument(context.Background(), map[string]any{"path": "format.go", "language": "go"})
	if err != nil {
		t.Fatalf("format preview: %v", err)
	}
	if preview["applied"] != false || preview["edit_count"].(int) == 0 {
		t.Fatalf("unexpected formatting preview: %#v", preview)
	}
	before, _ := os.ReadFile(source)
	if string(before) != original {
		t.Fatalf("preview modified source: %s", before)
	}

	applied, err := engine.FormatDocument(context.Background(), map[string]any{"path": "format.go", "language": "go", "apply": true})
	if err != nil {
		t.Fatalf("format apply: %v", err)
	}
	if applied["applied"] != true {
		t.Fatalf("format was not applied: %#v", applied)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "func Add(a int, b int) int { return a + b }") {
		t.Fatalf("gopls formatting not applied: %s", content)
	}
}
