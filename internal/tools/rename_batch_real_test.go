package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// The same method spelling can appear in independent Go interfaces. A
// batch must compute every LSP rename on unmodified source and apply the union
// in a single validated transaction, not partly update one interface.
func TestRealGoBatchSemanticRenameAtomic(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("set SIMPLE_LSP_REAL_LSP=1 for actual Go LSP")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	root := t.TempDir()
	write := func(path, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.org/batchrename\n\ngo 1.26\n")
	write("primary.go", `package rename
type Primary interface { Emit(string) string }
type Alpha struct{}
var _ Primary = Alpha{}
func (Alpha) Emit(value string) string { return value }
func UsePrimary(p Primary) string { return p.Emit("a") }
`)
	write("secondary.go", `package rename
type Secondary interface { Emit(int) int }
type Beta struct{}
var _ Secondary = Beta{}
func (Beta) Emit(value int) int { return value }
func UseSecondary(p Secondary) int { return p.Emit(1) }
`)
	write("third.go", `package rename
type Independent interface { Emit(bool) bool }
`)
	write("tagged.go", `//go:build experimentaltag

package rename
func AlternateCaller(p Primary) string { return p.Emit("tagged") }
`)
	excludedOS := "windows"
	if runtime.GOOS == "windows" {
		excludedOS = "linux"
	}
	alternateFile := "alternate_" + excludedOS + ".go"
	write(alternateFile, `package rename
func AlternateOSCaller(p Primary) string { return p.Emit("platform") }
`)
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{Workspace: root, RequestTimeout: 30 * time.Second,
		MaxResults: 100, Servers: map[string][]config.Server{"go": {{Command: "gopls", Directory: "."}}}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	targets := []any{
		map[string]any{"path": "primary.go", "symbol_path": "Primary/Emit"},
		map[string]any{"path": "secondary.go", "symbol_path": "Secondary/Emit"},
	}
	args := map[string]any{"targets": targets, "new_name": "Send"}
	preview, err := engine.PreviewRename(ctx, args)
	if err != nil {
		t.Fatalf("batch preview: %v", err)
	}
	if preview["applied"] != false || preview["targets_count"] != 2 {
		t.Fatalf("preview unexpectedly wrote/omitted targets: %#v", preview)
	}
	if preview["coverage_complete"] != false {
		t.Fatalf("tagged Go file must make rename coverage explicitly incomplete: %#v", preview)
	}
	tagged, ok := preview["unverified_build_tag_files"].([]string)
	if !ok || len(tagged) != 2 ||
		!(tagged[0] == "tagged.go" || tagged[1] == "tagged.go") ||
		!(tagged[0] == alternateFile || tagged[1] == alternateFile) {
		t.Fatalf("missed explicit/implicit Go build condition: %#v", preview)
	}
	candidates, ok := preview["unrenamed_method_candidates"].([]renameSiblingCandidate)
	if !ok {
		t.Fatalf("missing independent method candidates in real LSP result: %#v", preview)
	}
	var sawIndependent bool
	for _, candidate := range candidates {
		if candidate.Path == "third.go" && candidate.SymbolPath == "Independent/Emit" && candidate.Kind == "interface_method" {
			sawIndependent = true
		}
	}
	if !sawIndependent {
		t.Fatalf("missed unrelated interface declaration outside rename edit: %#v", candidates)
	}
	beforeA, _ := os.ReadFile(filepath.Join(root, "primary.go"))
	beforeB, _ := os.ReadFile(filepath.Join(root, "secondary.go"))
	if !strings.Contains(string(beforeA), "Emit(") || !strings.Contains(string(beforeB), "Emit(") {
		t.Fatal("read-only preview wrote source")
	}
	// An invalid second target must abort before the first method is renamed.
	bad := map[string]any{"targets": []any{targets[0], map[string]any{
		"path": "secondary.go", "symbol_path": "NoSuchInterface/Emit",
	}}, "new_name": "Send", "apply": true}
	if _, err := engine.RenameSymbol(ctx, bad); err == nil {
		t.Fatal("invalid batch target was not rejected")
	}
	afterA, _ := os.ReadFile(filepath.Join(root, "primary.go"))
	afterB, _ := os.ReadFile(filepath.Join(root, "secondary.go"))
	if string(afterA) != string(beforeA) || string(afterB) != string(beforeB) {
		t.Fatal("failed batch partially modified source")
	}
	args["apply"] = true
	applied, err := engine.RenameSymbol(ctx, args)
	if err != nil {
		t.Fatalf("batch apply: %v", err)
	}
	if applied["applied"] != true || applied["targets_count"] != 2 {
		t.Fatalf("batch not applied: %#v", applied)
	}
	if applied["coverage_complete"] != false {
		t.Fatalf("applied rename falsely claims complete build-tag coverage: %#v", applied)
	}
	leftout, err := os.ReadFile(filepath.Join(root, "tagged.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(leftout), "p.Emit") {
		t.Fatal("unresolved build-tag candidate silently rewritten as text")
	}
	alternate, err := os.ReadFile(filepath.Join(root, alternateFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(alternate), "p.Emit") {
		t.Fatal("unresolved GOOS-suffix source was silently rewritten")
	}
	unrelated, err := os.ReadFile(filepath.Join(root, "third.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unrelated), "Emit(bool)") {
		t.Fatal("independent unconfirmed interface was renamed without authorization")
	}
	for _, path := range []string{"primary.go", "secondary.go"} {
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "Emit(") || !strings.Contains(string(body), "Send(") {
			t.Fatalf("semantic batch left stale declarations/calls in %s: %s", path, body)
		}
	}
}
