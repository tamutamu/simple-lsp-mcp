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
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
)

// RenameSymbol asks the selected language server to compute a semantic rename.
// It is preview-only unless apply=true. The server never computes replacements
// itself; it only validates and optionally applies the returned WorkspaceEdit.
func (e *Engine) RenameSymbol(ctx context.Context, in map[string]any) (map[string]any, error) {
	newName := stringVal(in, "new_name")
	if strings.TrimSpace(newName) == "" {
		return nil, core.NewError(core.InvalidArgument, "new_name must be a non-empty string")
	}
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}
	caps := s.Capabilities()
	if !caps.Rename {
		return nil, e.unsupported(p, "textDocument/rename")
	}

	positionParams := map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}
	var preparation map[string]any
	if caps.PrepareRename {
		callCtx, cancel := e.callContext(ctx)
		var raw json.RawMessage
		err := s.Request(callCtx, "textDocument/prepareRename", positionParams, &raw)
		cancel()
		if err != nil {
			return nil, err
		}
		preparation, err = renamePreparation(d.Text, raw, caps.PositionEncoding)
		if err != nil {
			return nil, err
		}
	}

	// tsserver and Pyright may not include unopened project files in rename
	// immediately after the first didOpen. Warm only source files that contain
	// the old symbol spelling; the language server still computes every edit.
	// The filesystem scan is bounded and fails closed instead of accepting a
	// potentially partial semantic rename.
	if p.SessionKey == "typescript-javascript" || p.SessionKey == "python" {
		name := e.renameWarmName(in, d.Text, preparation)
		if name != "" {
			if err := e.warmRenameCandidates(ctx, p, s, name); err != nil {
				return nil, err
			}
		}
	}

	callCtx, cancel := e.callContext(ctx)
	var edit protocol.WorkspaceEdit
	err = s.Request(callCtx, "textDocument/rename", map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"position":     pos,
		"newName":      newName,
	}, &edit)
	cancel()
	if err != nil {
		return nil, err
	}
	plan, files, editCount, err := e.workspaceEditPlan(edit, caps.PositionEncoding)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"new_name": newName, "applied": false, "file_count": len(files), "edit_count": editCount, "files": files}
	if preparation != nil {
		result["prepare"] = preparation
	}
	apply, _ := in["apply"].(bool)
	if !apply {
		return result, nil
	}
	warnings, err := e.applyWorkspaceEditPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	result["applied"] = true
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

const (
	renameWarmMaxFiles   = 10000
	renameWarmMaxBytes   = 128 << 20
	renameWarmMaxMatches = 512
)

func (e *Engine) renameWarmName(in map[string]any, text []byte, preparation map[string]any) string {
	if preparation != nil {
		if placeholder, _ := preparation["placeholder"].(string); placeholder != "" {
			return placeholder
		}
		if r, ok := preparation["range"].(core.Range); ok {
			start, startErr := byteOffsetAtCorePosition(text, r.Start)
			end, endErr := byteOffsetAtCorePosition(text, r.End)
			if startErr == nil && endErr == nil && start < end && end <= len(text) {
				return string(text[start:end])
			}
		}
	}
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

func (e *Engine) warmRenameCandidates(ctx context.Context, p language.Profile, s *session.Session, name string) error {
	root := filepath.Clean(s.Root())
	needle := []byte(name)
	filesScanned, bytesScanned, matches := 0, int64(0), 0
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return core.WithCause(core.IncompleteSearch, "rename warm-up could not inspect project files", walkErr)
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
		if filesScanned > renameWarmMaxFiles {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("rename warm-up exceeded %d source files; narrow the workspace/profile before renaming", renameWarmMaxFiles))
		}
		info, err := entry.Info()
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "rename warm-up could not stat "+rel, err)
		}
		bytesScanned += info.Size()
		if bytesScanned > renameWarmMaxBytes {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("rename warm-up exceeded %d MiB of source; narrow the workspace/profile before renaming", renameWarmMaxBytes>>20))
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "rename warm-up could not read "+rel, err)
		}
		if !bytes.Contains(content, needle) {
			return nil
		}
		matches++
		if matches > renameWarmMaxMatches {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("rename warm-up found more than %d candidate files; narrow the workspace/profile before renaming", renameWarmMaxMatches))
		}
		syncCtx, cancel := e.callContext(ctx)
		_, err = e.Docs.Sync(syncCtx, s, path, candidateProfile.LanguageID)
		cancel()
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "rename warm-up could not open "+rel+" in the language server", err)
		}
		return nil
	})
}

func renamePreparation(text []byte, raw json.RawMessage, encoding string) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, core.NewError(core.InvalidArgument, "language server does not allow rename at the target")
	}
	var direct protocol.Range
	if json.Unmarshal(trimmed, &direct) == nil {
		var probe map[string]json.RawMessage
		if json.Unmarshal(trimmed, &probe) == nil && probe["start"] != nil && probe["end"] != nil {
			converted, err := rangeFromText(text, direct, encoding)
			if err != nil {
				return nil, err
			}
			return map[string]any{"range": converted}, nil
		}
	}
	var options struct {
		Range           *protocol.Range `json:"range,omitempty"`
		Placeholder     string          `json:"placeholder,omitempty"`
		DefaultBehavior bool            `json:"defaultBehavior,omitempty"`
	}
	if err := json.Unmarshal(trimmed, &options); err != nil {
		return nil, core.WithCause(core.InternalError, "invalid prepareRename response", err)
	}
	out := map[string]any{}
	if options.Range != nil {
		converted, err := rangeFromText(text, *options.Range, encoding)
		if err != nil {
			return nil, err
		}
		out["range"] = converted
	}
	if options.Placeholder != "" {
		out["placeholder"] = options.Placeholder
	}
	if options.DefaultBehavior {
		out["default_behavior"] = true
	}
	if len(out) == 0 {
		return nil, core.NewError(core.InvalidArgument, "language server returned an unusable prepareRename response")
	}
	return out, nil
}
