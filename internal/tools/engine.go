// Package tools implements the read-only MCP operations over LSP.
package tools

import (
	"context"
	"encoding/json"
	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
	"github.com/tamutamu/simple-lsp-mcp/internal/symbol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
	"os"
	"time"
)

type Engine struct {
	WS              *workspace.Workspace
	Sessions        *session.Manager
	Docs            *document.Store
	Symbols         *symbol.Registry
	Timeout         time.Duration
	DiagnosticsWait time.Duration
	MaxResults      int
}

func New(ws *workspace.Workspace, cfg config.Runtime) *Engine {
	return &Engine{WS: ws, Sessions: session.NewManager(ws.Root(), cfg.Servers), Docs: document.NewStore(), Symbols: symbol.New(), Timeout: cfg.RequestTimeout, DiagnosticsWait: cfg.DiagnosticsWait, MaxResults: cfg.MaxResults}
}
func (e *Engine) SearchSymbols(ctx context.Context, in map[string]any) (map[string]any, error) {
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	p, err := language.Require(stringVal(in, "language"))
	if err != nil {
		return nil, err
	}
	s, err := e.Sessions.For(p.SessionKey)
	if err != nil {
		return e.workspaceSymbols(nil, p, false, true, []string{err.Error()}), nil
	}

	callCtx, cancel := e.callContext(ctx)
	defer cancel()
	var raw []protocol.WorkspaceSymbol
	if err := s.Request(callCtx, "workspace/symbol", map[string]string{"query": stringVal(in, "query")}, &raw); err != nil {
		return nil, err
	}

	out := make([]core.SymbolSummary, 0, len(raw))
	for _, candidate := range raw {
		if !kindAllowed(normalize.Kind(candidate.Kind), in) {
			continue
		}
		summary, err := e.symbolFromWorkspace(p, candidate, s)
		if err != nil {
			continue
		}
		out = append(out, summary)
		if len(out) >= limit {
			return e.workspaceSymbols(out, p, true, false, nil), nil
		}
	}
	return e.workspaceSymbols(out, p, false, true, nil), nil
}

func (e *Engine) workspaceSymbols(symbols []core.SymbolSummary, profile language.Profile, truncated, includeServers bool, warnings []string) map[string]any {
	meta := core.Meta{Complete: true, Truncated: truncated, Warnings: warnings}
	if includeServers {
		meta.Servers = []string{profile.SessionKey}
	}
	return map[string]any{
		"symbols": symbols,
		"meta":    meta,
	}
}
func (e *Engine) DocumentSymbols(ctx context.Context, in map[string]any) (map[string]any, error) {
	path := stringVal(in, "path")
	p, s, d, err := e.document(ctx, path, stringVal(in, "language"))
	if err != nil {
		return nil, err
	}
	if !s.Capabilities().DocumentSymbol {
		return nil, e.unsupported(p, "textDocument/documentSymbol")
	}
	var raw json.RawMessage
	callCtx, cancel := e.callContext(ctx)
	err = s.Request(callCtx, "textDocument/documentSymbol", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}}, &raw)
	cancel()
	if err != nil {
		return nil, err
	}
	var tree []protocol.DocumentSymbol
	if json.Unmarshal(raw, &tree) == nil && tree != nil {
		return map[string]any{"symbols": e.documentTree(p, s, d, tree)}, nil
	}
	var flat []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, err
	}
	out := make([]core.SymbolSummary, 0, len(flat))
	for _, v := range flat {
		if x, err := e.symbolFromInfo(p, s, d, v); err == nil {
			out = append(out, x)
		}
	}
	return map[string]any{"symbols": out}, nil
}
func (e *Engine) GetSymbol(_ context.Context, in map[string]any) (map[string]any, error) {
	r, err := e.Symbols.Get(stringVal(in, "symbol_id"))
	if err != nil {
		return nil, err
	}
	full, err := e.WS.Resolve(r.Path)
	if err != nil {
		return nil, core.NewError(core.StaleSymbol, "symbol file no longer exists")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, core.NewError(core.StaleSymbol, "symbol file no longer exists")
	}
	h := hash(b)
	if h != r.FileHash {
		return nil, core.NewError(core.StaleSymbol, "symbol changed; search again")
	}
	v := core.SymbolSummary{SymbolID: r.ID, Name: r.Name, Kind: r.Kind, ContainerName: r.ContainerName, Language: languageName(r.SessionKey), Path: r.Path, Range: r.Range, SelectionRange: r.SelectionRange}
	m := map[string]any{"symbol": v, "meta": core.Meta{Complete: true}}
	if boolValDefault(in, "include_source", true) {
		source := sliceRange(b, r.Range)
		tr := false
		if len(source) > 64<<10 {
			source = source[:64<<10]
			tr = true
		}
		m["symbol"] = map[string]any{"symbol_id": v.SymbolID, "name": v.Name, "kind": v.Kind, "container_name": v.ContainerName, "language": v.Language, "path": v.Path, "range": v.Range, "selection_range": v.SelectionRange, "source": string(source)}
		m["meta"] = core.Meta{Complete: true, SourceTruncated: tr}
	}
	return m, nil
}
func (e *Engine) Relationship(ctx context.Context, name, method, cap string, in map[string]any) (map[string]any, error) {
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}
	if !capable(s.Capabilities(), cap) {
		return nil, e.unsupported(p, method)
	}
	params := map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}
	if name == "find_references" {
		params["context"] = map[string]bool{"includeDeclaration": boolValDefault(in, "include_declaration", false)}
	}
	var raw json.RawMessage
	callCtx, cancel := e.callContext(ctx)
	err = s.Request(callCtx, method, params, &raw)
	cancel()
	if err != nil {
		return nil, err
	}
	locs, err := e.locations(raw, s.Capabilities().PositionEncoding)
	if err != nil {
		return nil, err
	}
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	tr := len(locs) > limit
	if tr {
		locs = locs[:limit]
	}
	return map[string]any{"locations": locs, "meta": core.Meta{Complete: true, Truncated: tr}}, nil
}
func (e *Engine) Hierarchy(ctx context.Context, name string, in map[string]any) (map[string]any, error) {
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}
	call := name == "get_incoming_calls" || name == "get_outgoing_calls"
	if call && !s.Capabilities().CallHierarchy {
		return nil, e.unsupported(p, "textDocument/prepareCallHierarchy")
	}
	if !call && !s.Capabilities().TypeHierarchy {
		return nil, e.unsupported(p, "textDocument/prepareTypeHierarchy")
	}
	prepare := "textDocument/prepareTypeHierarchy"
	if call {
		prepare = "textDocument/prepareCallHierarchy"
	}
	var raw json.RawMessage
	callCtx, cancel := e.callContext(ctx)
	err = s.Request(callCtx, prepare, map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}, &raw)
	cancel()
	if err != nil {
		return nil, err
	}
	if call {
		return e.callHierarchy(ctx, name, p, s, raw, in)
	}
	return e.typeHierarchy(ctx, name, p, s, raw, in)
}
func (e *Engine) Diagnostics(ctx context.Context, in map[string]any) (map[string]any, error) {
	path := stringVal(in, "path")
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	var ds []core.Diagnostic
	complete := true
	if path != "" {
		p, s, d, err := e.document(ctx, path, stringVal(in, "language"))
		if err != nil {
			return nil, err
		}
		if s.Capabilities().Diagnostics {
			var raw struct {
				Items []protocol.Diagnostic `json:"items"`
			}
			callCtx, cancel := e.callContext(ctx)
			err = s.Request(callCtx, "textDocument/diagnostic", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "previousResultId": nil}, &raw)
			cancel()
			if err != nil {
				return nil, err
			}
			ds = e.diagnostics(d.Path, raw.Items, s.Capabilities().PositionEncoding)
		} else {
			time.Sleep(e.DiagnosticsWait)
			ds = e.diagnostics(d.Path, s.Diagnostics(d.URI), s.Capabilities().PositionEncoding)
		}
		_ = p
	} else {
		complete = false
	}
	ds = filterDiagnostics(ds, in)
	tr := len(ds) > limit
	if tr {
		ds = ds[:limit]
	}
	return map[string]any{"diagnostics": ds, "meta": core.Meta{Complete: complete, Truncated: tr}}, nil
}
