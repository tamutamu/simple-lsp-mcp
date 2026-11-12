// Command constructor-usage checks real Go AST call sites, excluding
// comments, strings, examples and unrelated New declarations.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Usage struct {
	OldCalls []string `json:"old_calls"`
	NewCalls []string `json:"new_calls"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run go_constructor_usage.go <repository-root>")
		os.Exit(2)
	}
	root := os.Args[1]
	var result Usage
	for _, sub := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, sub), func(name string, ent fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ent.IsDir() {
				if strings.HasPrefix(ent.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, name, nil, parser.AllErrors)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			aliases := map[string]bool{}
			for _, imp := range file.Imports {
				importPath, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if !strings.HasSuffix(importPath, "/internal/tools") {
					continue
				}
				alias := "tools"
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				if alias != "_" && alias != "." {
					aliases[alias] = true
				}
			}
			if len(aliases) == 0 {
				return nil
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				receiver, ok := sel.X.(*ast.Ident)
				if !ok || !aliases[receiver.Name] {
					return true
				}
				loc := fmt.Sprintf("%s:%d", filepath.ToSlash(strings.TrimPrefix(name, root+string(filepath.Separator))), fset.Position(call.Pos()).Line)
				switch sel.Sel.Name {
				case "New":
					result.OldCalls = append(result.OldCalls, loc)
				case "NewEngine":
					result.NewCalls = append(result.NewCalls, loc)
				}
				return true
			})
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	out, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
