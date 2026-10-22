package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestParseUnifiedDiffTracksCurrentLineRanges(t *testing.T) {
	patch := []byte("diff --git a.go a.go\n--- a.go\n+++ a.go\n@@ -2,2 +2,3 @@\n-old\n+new\n+more\n context\n@@ -10 +11,0 @@\n-deleted\n")
	files, err := parseUnifiedDiff(patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "a.go" {
		t.Fatalf("files = %#v", files)
	}
	want := []changedLineRange{{Start: 2, End: 4}, {Start: 11, End: 11}}
	if len(files[0].Ranges) != len(want) {
		t.Fatalf("ranges = %#v", files[0].Ranges)
	}
	for i := range want {
		if files[0].Ranges[i] != want[i] {
			t.Fatalf("range[%d] = %#v, want %#v", i, files[0].Ranges[i], want[i])
		}
	}
}

func TestParseUnifiedDiffHandlesDeletedFile(t *testing.T) {
	patch := []byte("diff --git gone.py gone.py\n--- gone.py\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n")
	files, err := parseUnifiedDiff(patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "gone.py" || !files[0].Deleted {
		t.Fatalf("files = %#v", files)
	}
}

func TestNodesForChangedRangesChoosesDeepestSymbols(t *testing.T) {
	nodes := []symbolNode{{
		Name: "Outer", SymbolPath: "Outer", Kind: "class",
		Range: coreRange(1, 20), SelectionRange: coreRange(1, 1),
		Children: []symbolNode{{
			Name: "method", SymbolPath: "Outer/method", Kind: "method",
			Range: coreRange(5, 10), SelectionRange: coreRange(5, 5),
		}},
	}}
	got := nodesForChangedRanges(nodes, []changedLineRange{{Start: 7, End: 7}})
	if len(got) != 1 || got[0].SymbolPath != "Outer/method" {
		t.Fatalf("nodes = %#v", got)
	}
}

func coreRange(start, end int) core.Range {
	return core.Range{Start: core.Position{Line: start, Column: 1}, End: core.Position{Line: end, Column: 1}}
}

func TestGitChangesIncludesTrackedAndUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.go", "package sample\n\nfunc A() int { return 1 }\n")
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "diff@example.invalid"}, {"config", "user.name", "Diff Test"}, {"add", "."}, {"commit", "-m", "baseline"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write("tracked.go", "package sample\n\nfunc A() int { return 2 }\n")
	write("new.go", "package sample\n\nfunc B() int { return 3 }\n")
	if err := os.MkdirAll(filepath.Join(root, ".scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(".scratch/generated.go", "package generated\n")
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{WS: ws}
	changes, err := engine.gitChanges(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 2 {
		t.Fatalf("files = %#v", changes.Files)
	}
	byPath := map[string]changedFile{}
	for _, file := range changes.Files {
		byPath[file.Path] = file
	}
	if len(byPath["tracked.go"].Ranges) == 0 {
		t.Fatalf("tracked diff missing ranges: %#v", byPath["tracked.go"])
	}
	if !byPath["new.go"].Untracked || len(byPath["new.go"].Ranges) != 1 {
		t.Fatalf("untracked file missing: %#v", byPath["new.go"])
	}
}

func TestDiffDiagnosticsUsesMessageIdentityNotLinePosition(t *testing.T) {
	before := []core.Diagnostic{{Path: "a.go", Severity: "error", Code: "E1", Source: "compiler", Message: "same error", Range: coreRange(3, 3)}}
	after := []core.Diagnostic{
		{Path: "a.go", Severity: "error", Code: "E1", Source: "compiler", Message: "same error", Range: coreRange(8, 8)},
		{Path: "a.go", Severity: "error", Code: "E2", Source: "compiler", Message: "new error", Range: coreRange(9, 9)},
	}
	newDiagnostics, resolved, unchanged := diffDiagnostics(before, after)
	if unchanged != 1 || len(newDiagnostics) != 1 || newDiagnostics[0].Code != "E2" || len(resolved) != 0 {
		t.Fatalf("new=%#v resolved=%#v unchanged=%d", newDiagnostics, resolved, unchanged)
	}
}

func TestPathExcludedFromWorkspaceCoversLocalToolState(t *testing.T) {
	for _, path := range []string{".local/state/gh/device-id", ".scratch/tmp.go", "node_modules/pkg/index.ts"} {
		if !pathExcludedFromWorkspace(path) {
			t.Fatalf("%s should be excluded", path)
		}
	}
	if pathExcludedFromWorkspace("internal/tools/verify.go") {
		t.Fatal("source path should not be excluded")
	}
}
