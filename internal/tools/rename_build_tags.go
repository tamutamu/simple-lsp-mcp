package tools

import (
	"bufio"
	"context"
	"go/build"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Go build tags can exclude source files from the active gopls package graph.
// A valid semantic WorkspaceEdit can therefore omit references that are only
// compiled with alternate tags. Report exact-name candidates in such files,
// without automatically changing identifiers we have not resolved semantically.
const maxTaggedRenameCandidates = 32

type renameTagScan struct {
	Paths     []string
	Truncated bool
}

func (e *Engine) taggedGoRenameCandidates(ctx context.Context, oldNames []string, edited []fileEditPreview) (renameTagScan, error) {
	var result renameTagScan
	names := make(map[string]bool, len(oldNames))
	for _, name := range oldNames {
		if name != "" {
			names[name] = true
		}
	}
	if len(names) == 0 {
		return result, nil
	}
	modified := make(map[string]bool, len(edited))
	for _, f := range edited {
		modified[f.Path] = true
	}
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
		relative, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if modified[relative] {
			return nil
		}
		// Check the header before reading a whole file. Go constraints must
		// appear in the leading comment region before the package clause.
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		constrained, headerErr := hasGoBuildConstraint(f)
		closeErr := f.Close()
		if headerErr != nil {
			return headerErr
		}
		if closeErr != nil {
			return closeErr
		}
		// Filename suffixes (_windows.go, _arm64.go, etc.) can also
		// exclude references from this gopls build even without //go:build.
		// Go's own matcher is authoritative for the current target.
		matched, matchErr := build.Default.MatchFile(filepath.Dir(path), entry.Name())
		if matchErr != nil {
			return matchErr
		}
		if !constrained && matched {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for name := range names {
			if warmContainsIdentifier(content, []byte(name)) {
				if len(result.Paths) == maxTaggedRenameCandidates {
					result.Truncated = true
					return nil
				}
				result.Paths = append(result.Paths, relative)
				break
			}
		}
		return nil
	})
	sort.Strings(result.Paths)
	return result, err
}

func hasGoBuildConstraint(source io.Reader) (bool, error) {
	reader := bufio.NewReader(source)
	for lines := 0; lines < 120; lines++ {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//go:build ") || strings.HasPrefix(trimmed, "// +build ") {
			return true, nil
		}
		if strings.HasPrefix(trimmed, "package ") {
			return false, nil
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}
