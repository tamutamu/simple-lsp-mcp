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

func TestRealLSPEditing(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") == "" {
		t.Skip("set SIMPLE_LSP_REAL_LSP=1")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/edit\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "main.go")
	testPath := filepath.Join(root, "main_test.go")
	actionPath := filepath.Join(root, "action.go")
	if err := os.WriteFile(mainPath, []byte("package edit\n\nimport (\n\t\"strings\"\n\t\"fmt\"\n)\n\nfunc OldName(a int,b int)string{return fmt.Sprint(a, strings.TrimSpace(\" x \"))}\nfunc UseName() string { return OldName(1, 2) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("package edit\n\nfunc ExampleOldName() { _ = OldName(1, 2) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(actionPath, []byte("package edit\n\nimport (\n\t\"strings\"\n\t\"fmt\"\n)\n\nfunc ActionValue() string { return fmt.Sprint(\"x\") }\n"), 0o600); err != nil {
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

	t.Run("rename_preview_and_apply", func(t *testing.T) {
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
			t.Fatalf("preview modified source: %s", before)
		}

		input["apply"] = true
		applied, err := engine.RenameSymbol(context.Background(), input)
		if err != nil {
			t.Fatalf("rename apply: %v", err)
		}
		if applied["applied"] != true {
			t.Fatalf("rename not applied: %#v", applied)
		}
		mainContent, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(mainContent), "func OldName") || strings.Contains(string(mainContent), "return OldName(") || !strings.Contains(string(mainContent), "func NewName") {
			t.Fatalf("semantic rename incomplete in main.go: %s", mainContent)
		}
		testContent, err := os.ReadFile(testPath)
		if err != nil {
			t.Fatal(err)
		}
		// gopls correctly renames the referenced symbol but not the unrelated
		// identifier ExampleOldName that merely contains the same text.
		if !strings.Contains(string(testContent), "ExampleOldName") || !strings.Contains(string(testContent), "NewName(1, 2)") {
			t.Fatalf("semantic rename changed the wrong identifiers: %s", testContent)
		}
	})

	t.Run("format_preview_and_apply", func(t *testing.T) {
		// Make the file deliberately ugly again after rename and let gopls
		// compute the formatting edits.
		content, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		ugly := strings.ReplaceAll(string(content), "func NewName(a int, b int) string {", "func NewName(a int,b int)string{")
		if err := os.WriteFile(mainPath, []byte(ugly), 0o600); err != nil {
			t.Fatal(err)
		}
		preview, err := engine.FormatDocument(context.Background(), map[string]any{"path": "main.go"})
		if err != nil {
			t.Fatalf("format preview: %v", err)
		}
		if preview["applied"] != false || preview["edit_count"].(int) == 0 {
			t.Fatalf("unexpected format preview: %#v", preview)
		}
		stillUgly, _ := os.ReadFile(mainPath)
		if !strings.Contains(string(stillUgly), "a int,b int") {
			t.Fatalf("format preview modified source: %s", stillUgly)
		}
		applied, err := engine.FormatDocument(context.Background(), map[string]any{"path": "main.go", "apply": true})
		if err != nil {
			t.Fatalf("format apply: %v", err)
		}
		if applied["applied"] != true {
			t.Fatalf("format not applied: %#v", applied)
		}
		formatted, _ := os.ReadFile(mainPath)
		if strings.Contains(string(formatted), "a int,b int") {
			t.Fatalf("gopls formatting was not applied: %s", formatted)
		}
	})

	t.Run("range_formatting", func(t *testing.T) {
		_, session, _, err := engine.document(context.Background(), "main.go", "go")
		if err != nil {
			t.Fatal(err)
		}
		if !session.Capabilities().RangeFormatting {
			t.Skip("gopls does not advertise textDocument/rangeFormatting")
		}
		content, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(content), "\n")
		ugly := "func UseName()string{return NewName(1,2)}"
		line := 0
		for i, value := range lines {
			if strings.HasPrefix(value, "func UseName") {
				lines[i] = ugly
				line = i + 1
				break
			}
		}
		if line == 0 {
			t.Fatalf("formatted fixture missing UseName: %s", content)
		}
		text := strings.Join(lines, "\n")
		if err := os.WriteFile(mainPath, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := engine.FormatDocument(context.Background(), map[string]any{
			"path": "main.go", "line": line, "column": 1, "end_line": line, "end_column": len([]rune(ugly)) + 1, "apply": true,
		})
		if err != nil {
			t.Fatalf("range format: %v", err)
		}
		if result["applied"] != true {
			t.Fatalf("range format not applied: %#v", result)
		}
		formatted, _ := os.ReadFile(mainPath)
		if strings.Contains(string(formatted), ugly) {
			t.Fatalf("range formatting did not update function: %s", formatted)
		}
	})

	t.Run("code_action_workspace_edit", func(t *testing.T) {
		actions, err := engine.GetCodeActions(context.Background(), map[string]any{
			"path":              "action.go",
			"line":              1,
			"column":            1,
			"end_line":          9,
			"end_column":        1,
			"code_action_kinds": []string{"source.organizeImports"},
		})
		if err != nil {
			t.Fatalf("get code actions: %v", err)
		}
		list, ok := actions["actions"].([]codeActionSummary)
		if !ok || len(list) == 0 {
			t.Fatalf("expected organize-imports action: %#v", actions)
		}
		selected := ""
		for _, action := range list {
			if action.Disabled == "" && (action.HasEdit || action.NeedsResolve) {
				selected = action.ActionID
				break
			}
		}
		if selected == "" {
			t.Skipf("gopls returned no WorkspaceEdit-backed organize-imports action: %#v", list)
		}
		applied, err := engine.ApplyCodeAction(context.Background(), map[string]any{
			"path":              "action.go",
			"line":              1,
			"column":            1,
			"end_line":          9,
			"end_column":        1,
			"code_action_kinds": []string{"source.organizeImports"},
			"action_id":         selected,
		})
		if err != nil {
			t.Fatalf("apply code action: %v", err)
		}
		if applied["applied"] != true {
			t.Fatalf("code action not applied: %#v", applied)
		}
		organized, err := os.ReadFile(actionPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(organized), "\"strings\"") {
			t.Fatalf("organize-imports action did not remove unused import: %s", organized)
		}
	})
}

func TestRealTypeScriptRangeFormatting(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("requires full real-LSP environment")
	}
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server is not installed")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true},"include":["*.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, "fixture.ts")
	source := "export function Add(a:number,b:number){return a+b}\n"
	if err := os.WriteFile(full, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	server := config.Server{Command: "typescript-language-server", Args: []string{"--stdio"}, Directory: "."}
	if path := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); path != "" {
		server.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": path}}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{RequestTimeout: 30 * time.Second, MaxResults: 100, Servers: map[string][]config.Server{"typescript-javascript": {server}}})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		engine.Sessions.Shutdown(ctx)
	})
	_, session, _, err := engine.document(context.Background(), "fixture.ts", "typescript")
	if err != nil {
		t.Fatal(err)
	}
	if !session.Capabilities().RangeFormatting {
		t.Skip("TypeScript Language Server does not advertise range formatting")
	}
	preview, err := engine.FormatDocument(context.Background(), map[string]any{
		"path": "fixture.ts", "language": "typescript",
		"line": 1, "column": 1, "end_line": 1, "end_column": len([]rune(strings.TrimSuffix(source, "\n"))) + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview["applied"] != false || preview["edit_count"].(int) == 0 {
		t.Fatalf("range format preview = %#v", preview)
	}
	applied, err := engine.FormatDocument(context.Background(), map[string]any{
		"path": "fixture.ts", "language": "typescript",
		"line": 1, "column": 1, "end_line": 1, "end_column": len([]rune(strings.TrimSuffix(source, "\n"))) + 1,
		"apply": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied["applied"] != true {
		t.Fatalf("range formatting not applied: %#v", applied)
	}
	formatted, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if string(formatted) == source {
		t.Fatalf("TypeScript range formatting made no change: %s", formatted)
	}
}

func TestRealSemanticRenameAcrossLanguages(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_REAL_LSP") != "1" {
		t.Skip("requires full real-LSP environment")
	}
	cases := []struct {
		name, profile, binary, setupName, setupContent, defName, defContent, refName, refContent, oldName, newName string
		args                                                                                                       []string
	}{
		{
			name: "typescript", profile: "typescript-javascript", binary: "typescript-language-server", args: []string{"--stdio"},
			setupName: "tsconfig.json", setupContent: `{"compilerOptions":{"strict":true,"moduleResolution":"Bundler","module":"ESNext"},"include":["*.ts"]}`,
			defName: "a.ts", defContent: "export function OldName(): number { return 1; }\n",
			refName: "b.ts", refContent: "import { OldName } from './a';\nexport const value = OldName();\n",
			oldName: "OldName", newName: "NewName",
		},
		{
			name: "python", profile: "python", binary: "pyright-langserver", args: []string{"--stdio"},
			setupName: "pyrightconfig.json", setupContent: `{"include":["."]}`,
			defName: "a.py", defContent: "def old_name() -> int:\n    return 1\n",
			refName: "b.py", refContent: "from a import old_name\nvalue = old_name()\n",
			oldName: "old_name", newName: "new_name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s is not installed", tc.binary)
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.setupName), []byte(tc.setupContent), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.defName), []byte(tc.defContent), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.refName), []byte(tc.refContent), 0o600); err != nil {
				t.Fatal(err)
			}
			server := config.Server{Command: tc.binary, Args: tc.args, Directory: "."}
			if tc.name == "typescript" {
				if path := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); path != "" {
					server.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": path}}
				}
			}
			ws, err := workspace.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			engine := New(ws, config.Runtime{RequestTimeout: 30 * time.Second, MaxResults: 100, Servers: map[string][]config.Server{tc.profile: {server}}})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				engine.Sessions.Shutdown(ctx)
			})
			references, err := engine.Relationship(context.Background(), "find_references", "textDocument/references", "references", map[string]any{
				"path": tc.defName, "symbol_path": tc.oldName, "include_declaration": true,
			})
			if err != nil {
				t.Fatalf("cross-file references: %v", err)
			}
			locations, _ := references["locations"].([]core.Location)
			if len(locations) < 2 {
				t.Fatalf("project warm-up missed unopened references: %#v", references)
			}

			incoming, err := engine.Hierarchy(context.Background(), "get_incoming_calls", map[string]any{
				"path": tc.defName, "symbol_path": tc.oldName,
			})
			if err != nil {
				t.Fatalf("cross-file incoming calls: %v", err)
			}
			calls, _ := incoming["calls"].([]any)
			if len(calls) < 1 {
				t.Fatalf("project warm-up missed unopened callers: %#v", incoming)
			}

			impact, err := engine.ImpactAnalysis(context.Background(), map[string]any{
				"path": tc.defName, "symbol_path": tc.oldName, "depth": 1,
			})
			if err != nil {
				t.Fatalf("cross-file impact analysis: %v", err)
			}
			affected, _ := impact["affected_files"].([]any)
			foundRef := false
			for _, raw := range affected {
				entry, _ := raw.(map[string]any)
				if entry["path"] == tc.refName {
					foundRef = true
					break
				}
			}
			if !foundRef {
				t.Fatalf("impact analysis missed unopened affected file %s: %#v", tc.refName, impact)
			}

			preview, err := engine.RenameSymbol(context.Background(), map[string]any{
				"path": tc.defName, "symbol_path": tc.oldName, "new_name": tc.newName,
			})
			if err != nil {
				t.Fatalf("rename preview: %v", err)
			}
			if preview["file_count"].(int) < 2 || preview["edit_count"].(int) < 2 {
				t.Fatalf("cross-file rename preview = %#v", preview)
			}
			applied, err := engine.RenameSymbol(context.Background(), map[string]any{
				"path": tc.defName, "symbol_path": tc.oldName, "new_name": tc.newName, "apply": true,
			})
			if err != nil {
				t.Fatalf("rename apply: %v", err)
			}
			if applied["applied"] != true {
				t.Fatalf("rename not applied: %#v", applied)
			}
			for _, name := range []string{tc.defName, tc.refName} {
				content, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(content), tc.oldName) || !strings.Contains(string(content), tc.newName) {
					t.Fatalf("%s semantic rename incomplete in %s: %s", tc.name, name, content)
				}
			}
		})
	}

	t.Run("typescript_path_alias_project_semantics", func(t *testing.T) {
		if _, err := exec.LookPath("typescript-language-server"); err != nil {
			t.Skip("typescript-language-server is not installed")
		}
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		tsconfig := `{"compilerOptions":{"strict":true,"moduleResolution":"Bundler","module":"ESNext","paths":{"@/*":["./src/*"]}},"include":["src"]}`
		if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(tsconfig), 0o600); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"src/theme.ts": `export const useTheme = () => ({ theme: "light" })` + "\n",
			"src/a.ts":     `import { useTheme } from "@/theme"` + "\n" + `export const A = () => useTheme().theme` + "\n",
			"src/b.ts":     `import { useTheme } from "@/theme"` + "\n" + `export const B = () => useTheme().theme` + "\n",
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		server := config.Server{Command: "typescript-language-server", Args: []string{"--stdio"}, Directory: "."}
		if path := os.Getenv("SIMPLE_LSP_TSSERVER_PATH"); path != "" {
			server.InitializationOptions = map[string]any{"tsserver": map[string]any{"path": path}}
		}
		ws, err := workspace.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		engine := New(ws, config.Runtime{RequestTimeout: 30 * time.Second, MaxResults: 100, Servers: map[string][]config.Server{"typescript-javascript": {server}}})
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			engine.Sessions.Shutdown(ctx)
		})
		target := map[string]any{"path": "src/theme.ts", "symbol_path": "useTheme", "include_declaration": true, "limit": 100}
		references, err := engine.Relationship(context.Background(), "find_references", "textDocument/references", "references", target)
		if err != nil {
			t.Fatalf("path-alias references: %v", err)
		}
		locations, _ := references["locations"].([]core.Location)
		if len(locations) < 5 {
			t.Fatalf("TypeScript project semantic prime missed path-alias references: %#v", references)
		}
		preview, err := engine.RenameSymbol(context.Background(), map[string]any{
			"path": "src/theme.ts", "symbol_path": "useTheme", "new_name": "useAppTheme",
		})
		if err != nil {
			t.Fatalf("path-alias rename preview: %v", err)
		}
		if preview["file_count"].(int) < 3 || preview["edit_count"].(int) < 5 {
			t.Fatalf("TypeScript project semantic prime produced partial rename: %#v", preview)
		}
	})

}
