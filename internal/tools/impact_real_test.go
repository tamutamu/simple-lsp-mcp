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

// This test exercises actual gopls documentSymbol + references, rather than
// only testing the in-memory BFS implementation with synthetic edges.
func TestRealImpactMultiHopGo(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" && os.Getenv("SIMPLE_LSP_REAL_LSP") != "go" {
		t.Skip("requires real gopls")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/impactfixture\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := "package fixture\nfunc Base() {}\nfunc Middle() { Base() }\nfunc Top() { Middle() }\nfunc Rec() { Rec() }\n"
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{
		Workspace: root, RequestTimeout: 30*time.Second, MaxResults: 100,
		Servers: map[string][]config.Server{"go": {{Command: "gopls", Directory: "."}}},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, tc := range []struct{
		depth, count int
		names []string
		complete bool
	}{
		{1, 2, []string{"Base", "Middle"}, false},
		{2, 3, []string{"Base", "Middle", "Top"}, false},
		{3, 3, []string{"Base", "Middle", "Top"}, true},
	} {
		res, err := engine.AnalyzeImpact(ctx, map[string]any{
			"path": "fixture.go", "line": 2, "depth": tc.depth,
		})
		if err != nil {
			t.Fatalf("depth=%d real impact: %v", tc.depth, err)
		}
		nodes, ok := res["nodes"].([]impactNode)
		if !ok || len(nodes) != tc.count {
			t.Fatalf("depth=%d unexpected nodes: %#v", tc.depth, res)
		}
		for i, name := range tc.names {
			if nodes[i].Name != name {
				t.Fatalf("depth=%d node[%d]=%q want %q", tc.depth, i, nodes[i].Name, name)
			}
		}
		meta := res["meta"].(map[string]any)
		if meta["complete"] != tc.complete {
			t.Fatalf("depth=%d completeness=%#v", tc.depth, meta)
		}
	}
	rec, err := engine.AnalyzeImpact(ctx, map[string]any{
		"path": "fixture.go", "line": 5, "depth": 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	recurNodes := rec["nodes"].([]impactNode)
	if len(recurNodes) != 1 || !strings.EqualFold(recurNodes[0].Name, "Rec") {
		t.Fatalf("recursive function was expanded incorrectly: %#v", rec)
	}
}
