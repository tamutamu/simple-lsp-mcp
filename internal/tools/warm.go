package tools

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
)

type projectWarmContextKey struct{}

func projectAlreadyWarmed(ctx context.Context) bool {
	warmed, _ := ctx.Value(projectWarmContextKey{}).(bool)
	return warmed
}

func markProjectWarmed(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectWarmContextKey{}, true)
}

const (
	projectWarmMaxFiles   = 10000
	projectWarmMaxBytes   = 128 << 20
	projectWarmMaxMatches = 512
)

func projectWarmSupported(p language.Profile) bool {
	return p.SessionKey == "typescript-javascript" || p.SessionKey == "python"
}

func (e *Engine) targetWarmName(in map[string]any) string {
	if symbolPath := stringVal(in, "symbol_path"); symbolPath != "" {
		if slash := strings.LastIndex(symbolPath, "/"); slash >= 0 {
			symbolPath = symbolPath[slash+1:]
		}
		return unescapeSegment(symbolPath)
	}
	if id := stringVal(in, "symbol_id"); id != "" {
		if record, err := e.Symbols.Get(id); err == nil {
			return record.Name
		}
	}
	return ""
}

// warmProjectSymbolCandidates opens only same-profile source files containing
// the target spelling. Project-based servers such as tsserver and Pyright can
// otherwise omit unopened files from references, incoming calls, subtypes, or
// rename immediately after the first didOpen. The scan is bounded and fails
// closed; semantic results still come exclusively from the language server.
func (e *Engine) warmProjectSymbolCandidates(ctx context.Context, p language.Profile, s *session.Session, name string) error {
	if name == "" || !projectWarmSupported(p) {
		return nil
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(s.Root()))
	if err != nil {
		return core.WithCause(core.InvalidPath, "project warm-up language-server root is invalid", err)
	}
	if _, err := e.WS.Relative(root); err != nil {
		return core.WithCause(core.InvalidPath, "project warm-up language-server root escapes the workspace", err)
	}
	needle := []byte(name)
	filesScanned, bytesScanned, matches := 0, int64(0), 0
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not inspect source files", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		candidateProfile, err := language.FromPath(rel)
		if err != nil || candidateProfile.SessionKey != p.SessionKey {
			return nil
		}
		filesScanned++
		if filesScanned > projectWarmMaxFiles {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d source files; narrow the workspace/profile", projectWarmMaxFiles))
		}
		info, err := entry.Info()
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not stat "+rel, err)
		}
		bytesScanned += info.Size()
		if bytesScanned > projectWarmMaxBytes {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d MiB of source; narrow the workspace/profile", projectWarmMaxBytes>>20))
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not read "+rel, err)
		}
		if !bytes.Contains(content, needle) {
			return nil
		}
		matches++
		if matches > projectWarmMaxMatches {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up found more than %d candidate files; narrow the workspace/profile", projectWarmMaxMatches))
		}
		syncCtx, cancel := e.callContext(ctx)
		_, err = e.Docs.Sync(syncCtx, s, path, candidateProfile.LanguageID)
		cancel()
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not open "+rel+" in the language server", err)
		}
		return nil
	})
}
