package workspace

import (
	"path/filepath"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// Workspace confines file access to a resolved workspace root.
type Workspace struct{ root string }

// Open resolves the root once so later path checks are symlink-aware.
func Open(path string) (*Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Workspace{root: real}, nil
}

// Root returns the resolved workspace root.
func (w *Workspace) Root() string { return w.root }

// Resolve returns a file inside the workspace and rejects escaping paths.
func (w *Workspace) Resolve(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "\\\\") || looksLikeDrive(path) {
		return "", core.NewError(core.InvalidPath, "path must be workspace-relative")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", core.NewError(core.InvalidPath, "path escapes workspace")
	}
	candidate := filepath.Join(w.root, clean)
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", core.NewError(core.InvalidPath, "path does not resolve to a file")
	}
	rel, err := filepath.Rel(w.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", core.NewError(core.InvalidPath, "path escapes workspace")
	}
	return real, nil
}

// Relative converts an in-workspace path into its workspace-relative form.
func (w *Workspace) Relative(path string) (string, error) {
	rel, err := filepath.Rel(w.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", core.NewError(core.InvalidPath, "path escapes workspace")
	}
	return filepath.ToSlash(rel), nil
}
func looksLikeDrive(p string) bool {
	return len(p) >= 2 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':'
}
