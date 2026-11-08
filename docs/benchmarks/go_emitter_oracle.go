// Command go_emitter_oracle enumerates explicit implementations of the
// one-method api/types/events.Emitter contract across ALL Go modules in a
// fixed checkout, independently of LSP workspace indexes.
//
// Usage: go run docs/benchmarks/go_emitter_oracle.go --root /path/to/teleport
//
// Limitations: source-only Go AST. Build tags, generated promoted methods,
// generic receiver substitution and dynamic registration require separate
// checks. The matched method signatures and the lib/events alias are verified.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type finding struct {
	Path     string `json:"path"`
	Receiver string `json:"receiver"`
	Method   string `json:"method"`
	Line     int    `json:"line"`
}

func selector(expr ast.Expr, pkg, name string) bool {
	s, ok := expr.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != name {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func receiverName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return receiverName(x.X)
	case *ast.IndexExpr:
		return receiverName(x.X)
	case *ast.IndexListExpr:
		return receiverName(x.X)
	}
	return ""
}

func methodMatches(f *ast.FuncDecl, pkgPath string, aliases map[string]string) bool {
	if f.Recv == nil || len(f.Recv.List) != 1 || f.Type == nil ||
		f.Type.Params == nil || f.Type.Results == nil {
		return false
	}
	var params []ast.Expr
	for _, field := range f.Type.Params.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			params = append(params, field.Type)
		}
	}
	if len(params) != 2 || !selector(params[0], "context", "Context") ||
		aliases["context"] != "context" ||
		len(f.Type.Results.List) != 1 {
		return false
	}
	errIdent, ok := f.Type.Results.List[0].Type.(*ast.Ident)
	if !ok || errIdent.Name != "error" {
		return false
	}
	switch audit := params[1].(type) {
	case *ast.Ident:
		// lib/events/types.go aliases this symbol to api/types/events.AuditEvent.
		return audit.Name == "AuditEvent" && pkgPath == "lib/events"
	case *ast.SelectorExpr:
		id, ok := audit.X.(*ast.Ident)
		if !ok || audit.Sel.Name != "AuditEvent" {
			return false
		}
		path := aliases[id.Name]
		return path == "github.com/gravitational/teleport/lib/events" ||
			path == "github.com/gravitational/teleport/api/types/events"
	default:
		return false
	}
}

func importsFor(file *ast.File) map[string]string {
	out := make(map[string]string)
	for _, item := range file.Imports {
		path, err := strconv.Unquote(item.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if item.Name != nil {
			name = item.Name.Name
		}
		out[name] = path
	}
	return out
}

func scan(root string) ([]finding, error) {
	result := make([]finding, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				switch entry.Name() {
				case ".git", ".bench-cache", "node_modules", "vendor", ".scratch":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("cannot parse %s: %w", relative, err)
		}
		pkg := filepath.ToSlash(filepath.Dir(relative))
		aliases := importsFor(file)
		for _, declaration := range file.Decls {
			method, ok := declaration.(*ast.FuncDecl)
			if !ok || method.Name.Name != "EmitAuditEvent" ||
				method.Recv == nil || len(method.Recv.List) == 0 {
				continue
			}
			if !methodMatches(method, pkg, aliases) {
				continue
			}
			result = append(result, finding{
				Path:     filepath.ToSlash(relative),
				Receiver: receiverName(method.Recv.List[0].Type),
				Method:   "EmitAuditEvent",
				Line:     set.Position(method.Name.Pos()).Line,
			})
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Receiver < result[j].Receiver
	})
	return result, err
}

func main() {
	root := flag.String("root", "", "fixed Teleport source directory")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "--root is required")
		os.Exit(2)
	}
	clean, err := filepath.Abs(*root)
	if err != nil {
		panic(err)
	}
	found, err := scan(clean)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(found)
}
