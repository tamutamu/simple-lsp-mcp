package tools

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// renameSiblingCandidate is a syntactically confirmed method declaration that
// the LSP WorkspaceEdit did not rename. A shared spelling is NOT proof of an
// interface relationship: these are candidates for agent review, not edits.
type renameSiblingCandidate struct {
	Path       string `json:"path"`
	SymbolPath string `json:"symbol_path"`
	Kind       string `json:"kind"`
	Generated  bool   `json:"generated,omitempty"`
}

type renameSiblingScan struct {
	Candidates  []renameSiblingCandidate
	Truncated   bool
	ParseErrors int
}

const maxRenameSiblingCandidates = 16

// findUnrenamedGoMethods is a bounded Go-AST discovery supplement to
// textDocument/rename. It solves a common failure mode: several independent Go
// interfaces or concrete types expose identically spelled methods, and gopls
// correctly renames only one semantic identity. No candidate is auto-edited.
func (e *Engine) findUnrenamedGoMethods(ctx context.Context, oldName string, edits []fileEditPreview) (renameSiblingScan, error) {
	var out renameSiblingScan
	if oldName == "" {
		return out, nil
	}
	covered := make(map[string][]editPreview, len(edits))
	for _, file := range edits {
		covered[file.Path] = file.Edits
	}
	needle := []byte(oldName)
	err := filepath.WalkDir(e.WS.Root(), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != e.WS.Root() && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(content, needle) {
			return nil
		}
		header := content
		if len(header) > 2048 {
			header = header[:2048]
		}
		isGenerated := bytes.Contains(header, []byte("Code generated")) &&
			bytes.Contains(header, []byte("DO NOT EDIT"))
		rel, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		set := token.NewFileSet()
		unit, parseErr := parser.ParseFile(set, path, content, parser.SkipObjectResolution)
		if parseErr != nil {
			out.ParseErrors++
			return nil
		}
		add := func(symbolPath, kind string, ident *ast.Ident) {
			if ident == nil || ident.Name != oldName {
				return
			}
			position := set.Position(ident.Pos())
			for _, change := range covered[rel] {
				// These 1-based line/column coordinates are shared by Go's
				// token.Position and the public editPreview type.
				if change.Range.Start.Line == position.Line &&
					change.Range.Start.Column == position.Column {
					return
				}
			}
			candidate := renameSiblingCandidate{
				Path: rel, SymbolPath: symbolPath, Kind: kind, Generated: isGenerated,
			}
			if len(out.Candidates) == maxRenameSiblingCandidates {
				out.Truncated = true
				// Keep source-owned declarations visible even when generated
				// protocol stubs appear earlier in a large workspace.
				if !isGenerated {
					for j := len(out.Candidates) - 1; j >= 0; j-- {
						if out.Candidates[j].Generated {
							out.Candidates[j] = candidate
							break
						}
					}
				}
				return
			}
			out.Candidates = append(out.Candidates, candidate)
		}
		for _, decl := range unit.Decls {
			switch node := decl.(type) {
			case *ast.GenDecl:
				for _, item := range node.Specs {
					spec, ok := item.(*ast.TypeSpec)
					if !ok {
						continue
					}
					iface, ok := spec.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					for _, field := range iface.Methods.List {
						if _, ok := field.Type.(*ast.FuncType); !ok {
							continue
						}
						for _, ident := range field.Names {
							add(spec.Name.Name+"/"+ident.Name, "interface_method", ident)
						}
					}
				}
			case *ast.FuncDecl:
				if node.Recv == nil || len(node.Recv.List) == 0 {
					continue
				}
				receiver := goReceiverName(node.Recv.List[0].Type)
				if receiver != "" {
					add(receiver+"/"+node.Name.Name, "receiver_method", node.Name)
				}
			}
		}
		return nil
	})
	sort.Slice(out.Candidates, func(i, j int) bool {
		if out.Candidates[i].Generated != out.Candidates[j].Generated {
			return !out.Candidates[i].Generated
		}
		if out.Candidates[i].Path != out.Candidates[j].Path {
			return out.Candidates[i].Path < out.Candidates[j].Path
		}
		return out.Candidates[i].SymbolPath < out.Candidates[j].SymbolPath
	})
	return out, err
}

func goReceiverName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return goReceiverName(v.X)
	case *ast.IndexExpr:
		return goReceiverName(v.X)
	case *ast.IndexListExpr:
		return goReceiverName(v.X)
	}
	return ""
}
