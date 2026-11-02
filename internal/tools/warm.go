package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

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
		return warmSymbolPathSpelling(unescapeSegment(symbolPath))
	}
	if id := stringVal(in, "symbol_id"); id != "" {
		if record, err := e.Symbols.Get(id); err == nil {
			return record.Name
		}
	}
	return ""
}

// warmSymbolPathSpelling converts a display path leaf back to a source token.
// DocumentSymbol sibling disambiguation adds "#2", "#3", etc. to repeated
// names, while flat LSP symbols can be receiver-qualified (e.g.
// "(*Engine).FindSymbol"). Neither synthetic spelling occurs in source
// references. The warm scan is only a candidate filter; the LSP remains the
// authority for identity and actual edit locations.
func warmSymbolPathSpelling(leaf string) string {
	if hash := strings.LastIndexByte(leaf, '#'); hash >= 0 && hash+1 < len(leaf) {
		if ordinal, err := strconv.Atoi(leaf[hash+1:]); err == nil && ordinal >= 2 {
			leaf = leaf[:hash]
		}
	}
	if dot := strings.LastIndexByte(leaf, '.'); dot >= 0 && dot+1 < len(leaf) {
		candidate := leaf[dot+1:]
		valid := true
		for _, r := range candidate {
			if !warmIdentRune(r) {
				valid = false
				break
			}
		}
		if valid {
			return candidate
		}
	}
	return leaf
}

// warmNameForPosition identifies the source spelling used to select project
// candidates. It does NOT determine symbol identity: only the subsequent LSP
// references/rename call does that. Taking the spelling from the already synced
// document avoids a separate prepareRename round trip on normal identifiers
// and works on servers where prepareRename is not advertised.
func (e *Engine) warmNameForPosition(ctx context.Context, p language.Profile, s *session.Session, d document.Document, pos protocol.Position, in map[string]any) string {
	if name := e.targetWarmName(in); name != "" {
		return name
	}
	if !projectWarmSupported(p) {
		return ""
	}
	if name := warmIdentifierAtPosition(d.Text, pos, s.Capabilities().PositionEncoding); name != "" {
		return name
	}
	// Fallback for language-specific spellings that a lexical identifier
	// detector cannot recognize. An LSP may not advertise prepareRename.
	if !s.Capabilities().PrepareRename {
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

// warmIdentifierAtPosition extracts only a potential token spelling using the
// LSP's negotiated UTF-8/16/32 position encoding. This is a *candidate filter*,
// never a substitute for the language server's semantic resolution.
func warmIdentifierAtPosition(text []byte, position protocol.Position, encoding string) string {
	point, sourceLine, err := document.FromLSPWithLine(text, position, encoding)
	if err != nil {
		return ""
	}
	// Do not split and allocate the entire source for a single cursor.
	// FromLSPWithLine already validated and extracted the requested line.
	line := []rune(sourceLine)
	offset := point.Column - 1
	// JavaScript identifiers may spell a character as \\uXXXX or \\u{...}.
	// Decode the token and cursor together so a position inside an escape
	// still yields the semantic spelling used by cross-file warm-up.
	line, offset = warmDecodeLinePosition(line, offset)
	if offset == len(line) && offset > 0 {
		offset--
	}
	if offset >= 0 && offset < len(line) && !warmIdentRune(line[offset]) &&
		offset > 0 && warmIdentRune(line[offset-1]) {
		offset--
	}
	if offset < 0 || offset >= len(line) || !warmIdentRune(line[offset]) {
		return ""
	}
	start, end := offset, offset+1
	for start > 0 && warmIdentRune(line[start-1]) {
		start--
	}
	for end < len(line) && warmIdentRune(line[end]) {
		end++
	}
	return string(line[start:end])
}

func warmIdentRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
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
	_, err = e.scanProjectWarmCandidates(ctx, p, root, []byte(name),
		func(path, rel string, candidateProfile language.Profile, content []byte) error {
			syncCtx, cancel := e.callContext(ctx)
			_, err := e.Docs.SyncBytes(syncCtx, s, path, candidateProfile.LanguageID, content)
			cancel()
			if err != nil {
				return core.WithCause(core.IncompleteSearch, "project warm-up could not open "+rel+" in the language server", err)
			}
			return nil
		})
	return err
}
