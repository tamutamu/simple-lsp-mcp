package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
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

// primeProjectSemantics forces project-based TypeScript servers to finish
// attaching the newly-opened candidate files before a project-wide relation or
// rename is requested. didOpen alone is asynchronous inside tsserver. A real
// call-hierarchy round trip proved to be the reliable semantic synchronization
// point; its result is discarded because the caller will make its own query.
func (e *Engine) primeProjectSemantics(ctx context.Context, p language.Profile, s *session.Session, d document.Document, pos protocol.Position) error {
	if p.SessionKey != "typescript-javascript" || !s.Capabilities().CallHierarchy {
		return nil
	}
	callCtx, cancel := e.callContext(ctx)
	var raw json.RawMessage
	err := s.Request(callCtx, "textDocument/prepareCallHierarchy", map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"position":     pos,
	}, &raw)
	cancel()
	if err != nil {
		return core.WithCause(core.IncompleteSearch, "project semantic warm-up could not prepare call hierarchy", err)
	}
	var items []protocol.CallHierarchyItem
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil
	}
	item := items[0]
	callCtx, cancel = e.callContext(ctx)
	var ignored json.RawMessage
	err = s.Request(callCtx, "callHierarchy/incomingCalls", map[string]any{"item": item}, &ignored)
	cancel()
	if err != nil {
		return core.WithCause(core.IncompleteSearch, "project semantic warm-up could not load incoming calls", err)
	}
	return nil
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

// warmNameForPosition asks the LSP for the exact identifier at a position.
// A path/line/column caller has no symbol name in its arguments, but project
// servers may omit unopened files unless candidate files are synchronized.
// prepareRename is a read-only LSP request and is not used to perform an edit.
func (e *Engine) warmNameForPosition(ctx context.Context, p language.Profile, s *session.Session, d document.Document, pos protocol.Position, in map[string]any) string {
	if name := e.targetWarmName(in); name != "" {
		return name
	}
	if !projectWarmSupported(p) || !s.Capabilities().PrepareRename {
		return ""
	}
	callCtx, cancel := e.callContext(ctx)
	var raw json.RawMessage
	err := s.Request(callCtx, "textDocument/prepareRename", map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"position":     pos,
	}, &raw)
	cancel()
	if err != nil {
		return ""
	}
	preparation, err := renamePreparation(d.Text, raw, s.Capabilities().PositionEncoding)
	if err != nil {
		return ""
	}
	return e.renameWarmName(in, d.Text, preparation)
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