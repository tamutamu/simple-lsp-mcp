package tools

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
)

// goModuleBoundaries is a conservative source-tree check. A Go LSP session
// rooted at one module can return complete=true for implementations confined
// to that module while missing compatible methods in another nested module.
// Multiple go.mod files are not proof of missing results, but they mean a
// single textDocument/implementation reply is not proof of workspace-wide
// completeness. This is deliberately not an alternate implementation oracle.
func goModuleBoundaries(ctx context.Context, root string) ([]string, error) {
	modules := make([]string, 0, 2)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			if path != root && entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || entry.Name() != "go.mod" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		modules = append(modules, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(modules)
	return modules, err
}
