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
type Independent interface { Emit(bool) bool; Other(bool) bool }
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
		map[string]any{"path": "primary.go", "symbol_path": "Alpha/Emit"},
	}
	// An agent often supplies language="go" once for an entire inspection
	// batch. Accept this shared hint instead of causing an extra tool turn.
	inspected, err := engine.InspectSymbol(ctx, map[string]any{
		"targets": []any{
			map[string]any{"path": "primary.go", "symbol_path": "Primary/Emit"},
			map[string]any{"path": "secondary.go", "symbol_path": "Secondary/Emit"},
		},
		"language": "go", "view": "signature",
	})
	if err != nil {
		t.Fatalf("shared language hint in multi-inspect failed: %v", err)
	}
	if results, ok := inspected["results"].([]map[string]any); !ok || len(results) != 2 {
		t.Fatalf("multi-inspect lost targets: %#v", inspected)
	}
	args := map[string]any{"targets": targets, "new_name": "Send"}
	preview, err := engine.PreviewRename(ctx, args)
	if err != nil {
		t.Fatalf("batch preview: %v", err)
	}
	if preview["applied"] != false || preview["targets_count"] != 3 {
		t.Fatalf("preview unexpectedly wrote/omitted targets: %#v", preview)
	}
	if covered, ok := preview["covered_target_indices"].([]int); !ok || len(covered) != 1 || covered[0] != 2 {
		t.Fatalf("implementation target already covered by interface rename was not skipped: %#v", preview)
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
	mixed := map[string]any{"targets": []any{targets[0], map[string]any{
		"path": "third.go", "symbol_path": "Independent/Other",
	}}, "new_name": "Send", "apply": true}
	if _, err := engine.RenameSymbol(ctx, mixed); err == nil {
		t.Fatal("mixed method names in a batch were silently merged")
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
	if applied["applied"] != true || applied["targets_count"] != 3 {
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

	// A separate workspace is needed to exercise a reversed, naturally
	// ordered batch because the original fixture has already been changed.
	// This request combines top-level Primary and explicit targets and also
	// places Beta's implementation BEFORE its interface. gopls rejects that
	// concrete rename alone until Secondary is processed.
	reversedRoot := t.TempDir()
	for _, path := range []string{"go.mod", "primary.go", "secondary.go", "third.go"} {
		var source []byte
		switch path {
		case "go.mod":
			source = []byte("module example.org/reorderedrename\n\ngo 1.26\n")
		case "primary.go":
			source = beforeA
		case "secondary.go":
			source = beforeB
		case "third.go":
			source = []byte("package rename\ntype Independent interface { Emit(bool) bool; Other(bool) bool }\n")
		}
		if err := os.WriteFile(filepath.Join(reversedRoot, path), source, 0600); err != nil {
			t.Fatal(err)
		}
	}
	reversedWS, err := workspace.Open(reversedRoot)
	if err != nil {
		t.Fatal(err)
	}
	reversedEngine := New(reversedWS, config.Runtime{Workspace: reversedRoot, RequestTimeout: 30 * time.Second,
		MaxResults: 100, Servers: map[string][]config.Server{"go": {{Command: "gopls", Directory: "."}}}})
	t.Cleanup(func() {
		c, closeFn := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeFn()
		reversedEngine.Sessions.Shutdown(c)
	})
	naturalBatch := map[string]any{
		"path": "primary.go", "symbol_path": "Primary/Emit", "language": "go",
		"targets": []any{
			map[string]any{"path": "secondary.go", "symbol_path": "Beta/Emit"},
			map[string]any{"path": "secondary.go", "symbol_path": "Secondary/Emit"},
		},
		"new_name": "Send", "apply": false,
	}
	previewNatural, err := reversedEngine.PreviewRename(ctx, naturalBatch)
	if err != nil {
		t.Fatalf("top-level anchor with reversed implementation/interface failed: %v", err)
	}
	if previewNatural["targets_count"] != 3 {
		t.Fatalf("normalization lost target: %#v", previewNatural)
	}
	if covered, ok := previewNatural["covered_target_indices"].([]int); !ok || len(covered) != 1 || covered[0] != 1 {
		t.Fatalf("concrete implementation not deferred until its interface: %#v", previewNatural)
	}
	// Repeating the explicit top-level identity inside targets[] should not
	// double-rename it or inflate the target count.
	duplicatedTop := map[string]any{
		"path": "primary.go", "symbol_path": "Primary/Emit", "language": "go",
		"targets": []any{
			map[string]any{"path": "primary.go", "symbol_path": "Primary/Emit"},
			map[string]any{"path": "secondary.go", "symbol_path": "Beta/Emit"},
			map[string]any{"path": "secondary.go", "symbol_path": "Secondary/Emit"},
		},
		"new_name": "Send",
	}
	duplicatesPreview, err := reversedEngine.PreviewRename(ctx, duplicatedTop)
	if err != nil || duplicatesPreview["targets_count"] != 3 {
		t.Fatalf("duplicated explicit anchor was not normalized: %#v: %v", duplicatesPreview, err)
	}
	naturalBatch["apply"] = true
	appliedNatural, err := reversedEngine.RenameSymbol(ctx, naturalBatch)
	if err != nil {
		t.Fatalf("reordered natural Go rename apply failed: %v", err)
	}
	if appliedNatural["applied"] != true {
		t.Fatalf("natural Go batch not applied: %#v", appliedNatural)
	}
	for _, path := range []string{"primary.go", "secondary.go"} {
		body, err := os.ReadFile(filepath.Join(reversedRoot, path))
		if err != nil || strings.Contains(string(body), "Emit(") || !strings.Contains(string(body), "Send(") {
			t.Fatalf("natural batch left old identifier in %s: %s; %v", path, body, err)
		}
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
