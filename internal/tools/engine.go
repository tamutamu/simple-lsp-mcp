// Package tools implements semantic navigation and explicit LSP-backed edit operations.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/transport"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
	"github.com/tamutamu/simple-lsp-mcp/internal/symbol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// Engine coordinates MCP tool requests with workspace, document, and LSP state.
type Engine struct {
	WS              *workspace.Workspace
	runtime         config.Runtime
	Sessions        *session.Manager
	Docs            *document.Store
	Symbols         *symbol.Registry
	Timeout         time.Duration
	DiagnosticsWait time.Duration
	MaxResults      int
}

// New builds an engine with session-local document and symbol stores.
func New(ws *workspace.Workspace, cfg config.Runtime) *Engine {
	return &Engine{
		WS:              ws,
		runtime:         cfg,
		Sessions:        session.NewManager(ws.Root(), cfg.Servers),
		Docs:            document.NewStore(),
		Symbols:         symbol.New(),
		Timeout:         cfg.RequestTimeout,
		DiagnosticsWait: cfg.DiagnosticsWait,
		MaxResults:      cfg.MaxResults,
	}
}

// SearchSymbols queries every configured LSP for the selected language, not
// just the first monorepo root. Failure of one server is an incomplete search
// and must not masquerade as a successful empty or unique result.
func (e *Engine) SearchSymbols(ctx context.Context, in map[string]any) (map[string]any, error) {
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	query := stringVal(in, "query")
	if query == "" {
		return nil, core.NewError(core.InvalidArgument, "query must be a non-empty string")
	}
	p, err := language.Require(stringVal(in, "language"))
	if err != nil {
		return nil, err
	}
	servers, err := e.Sessions.ForAll(p.SessionKey)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	all := make([]core.SymbolSummary, 0)
	for _, server := range servers {
		callCtx, cancel := e.callContext(ctx)
		var raw []protocol.WorkspaceSymbol
		err := server.Request(callCtx, "workspace/symbol", map[string]string{"query": query}, &raw)
		cancel()
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "workspace symbol query failed in "+p.SessionKey, err)
		}
		for _, candidate := range raw {
			if !kindAllowed(normalize.Kind(candidate.Kind), in) {
				continue
			}
			// LSP workspace results can include standard-library and dependency
			// symbols outside our workspace. They are intentionally out of scope.
			if _, err := normalize.URIPath(e.WS, candidate.Location.URI); err != nil {
				continue
			}
			summary, err := e.symbolFromWorkspace(p, candidate, server)
			if err != nil {
				return nil, core.WithCause(core.IncompleteSearch, "could not decode workspace symbol", err)
			}
			key := fmt.Sprintf("%s:%v:%s", summary.Path, summary.SelectionRange, summary.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, summary)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		if all[i].Range.Start.Line != all[j].Range.Start.Line {
			return all[i].Range.Start.Line < all[j].Range.Start.Line
		}
		return all[i].Name < all[j].Name
	})
	truncated := len(all) > limit
	if truncated {
		all = all[:limit]
	}
	return map[string]any{"symbols": all, "meta": core.Meta{Complete: true, Truncated: truncated, Servers: []string{p.SessionKey}}}, nil
}

// DocumentSymbols returns hierarchical symbols when supported by the server.
func (e *Engine) DocumentSymbols(ctx context.Context, in map[string]any) (map[string]any, error) {
	path := stringVal(in, "path")
	p, s, d, err := e.document(ctx, path, stringVal(in, "language"))
	if err != nil {
		return nil, err
	}
	raw, err := e.requestDocumentSymbolTree(ctx, p, s, d)
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

// requestDocumentSymbolTree fetches the raw textDocument/documentSymbol
// response for an already-resolved document, in whichever of the two
// shapes (hierarchical or flat) the server returns.
func (e *Engine) requestDocumentSymbolTree(ctx context.Context, p language.Profile, s *session.Session, d document.Document) (json.RawMessage, error) {
	if !s.Capabilities().DocumentSymbol {
		return nil, e.unsupported(p, "textDocument/documentSymbol")
	}
	var raw json.RawMessage
	callCtx, cancel := e.callContext(ctx)
	err := s.Request(callCtx, "textDocument/documentSymbol", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}}, &raw)
	cancel()
	return raw, err
}

// Relationship resolves a target and invokes one location-based LSP method.
func (e *Engine) Relationship(ctx context.Context, name, method, cap string, in map[string]any) (map[string]any, error) {
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}
	if !capable(s.Capabilities(), cap) {
		return nil, e.unsupported(p, method)
	}
	if !projectAlreadyWarmed(ctx) && (name == "find_references" || name == "find_implementations") {
		if err := e.warmProjectSymbolCandidates(ctx, p, s, e.warmNameForPosition(ctx, p, s, d, pos, in)); err != nil {
			return nil, err
		}
		if err := e.primeProjectSemantics(ctx, p, s, d, pos); err != nil {
			return nil, err
		}
	}
	var extra map[string]any
	if name == "find_references" {
		extra = map[string]any{"context": map[string]bool{"includeDeclaration": boolValDefault(in, "include_declaration", false)}}
	}
	locs, err := e.locationsAt(ctx, nil, s, d.URI, pos, method, extra)
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

// locationsAt runs one location-based LSP method against an already-resolved
// target without re-resolving or re-syncing the document.
func (e *Engine) locationsAt(ctx context.Context, c *fileCache, s *session.Session, uri string, pos protocol.Position, method string, extra map[string]any) ([]core.Location, error) {
	params := map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: uri}, "position": pos}
	for k, v := range extra {
		params[k] = v
	}
	var raw json.RawMessage
	callCtx, cancel := e.callContext(ctx)
	err := s.Request(callCtx, method, params, &raw)
	cancel()
	if err != nil {
		return nil, err
	}
	return e.locationsIn(c, raw, s.Capabilities().PositionEncoding)
}

// Hierarchy prepares a call or type hierarchy before reading one level.
func (e *Engine) Hierarchy(ctx context.Context, name string, in map[string]any) (map[string]any, error) {
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}
	call := name == "get_incoming_calls" || name == "get_outgoing_calls"
	if !projectAlreadyWarmed(ctx) && (name == "get_incoming_calls" || name == "get_subtypes") {
		if err := e.warmProjectSymbolCandidates(ctx, p, s, e.warmNameForPosition(ctx, p, s, d, pos, in)); err != nil {
			return nil, err
		}
	}
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

// Diagnostics uses pull diagnostics when available and push diagnostics otherwise.
func (e *Engine) Diagnostics(ctx context.Context, in map[string]any) (map[string]any, error) {
	path := stringVal(in, "path")
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	var ds []core.Diagnostic
	complete := true
	if path != "" {
		_, s, d, err := e.document(ctx, path, stringVal(in, "language"))
		if err != nil {
			return nil, err
		}
		var raw struct {
			Items []protocol.Diagnostic `json:"items"`
		}
		callCtx, cancel := e.callContext(ctx)
		pullErr := s.Request(callCtx, "textDocument/diagnostic", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "previousResultId": nil}, &raw)
		cancel()
		diagnosticPath := relativeMust(e, d.Path)
		if pullErr == nil {
			ds = e.diagnostics(diagnosticPath, raw.Items, s.Capabilities().PositionEncoding)
		} else {
			var protocolErr *transport.ProtocolError
			unsupported := errors.As(pullErr, &protocolErr) && protocolErr.Code == -32601
			// Some servers (including gopls versions in the real-LSP suite)
			// implement pull diagnostics without advertising diagnosticProvider.
			// Probe first; when an unadvertised server rejects the method, fall
			// back to its publishDiagnostics cache. Advertised pull failures remain
			// real errors and must not be hidden.
			if s.Capabilities().Diagnostics && !unsupported {
				return nil, pullErr
			}
			if e.DiagnosticsWait > 0 {
				timer := time.NewTimer(e.DiagnosticsWait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			ds = e.diagnostics(diagnosticPath, s.Diagnostics(d.URI), s.Capabilities().PositionEncoding)
		}
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