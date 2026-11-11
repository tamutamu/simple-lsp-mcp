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
	warmIndex       warmSourceIndex
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
	detail, err := symbolListDetail(in)
	if err != nil {
		return nil, err
	}
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
	sourceCache := newFileCache()
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
			summary, err := e.symbolFromWorkspaceIn(sourceCache, p, candidate, server)
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
	return map[string]any{"symbols": projectSymbols(all, detail), "meta": core.Meta{Complete: !truncated, Truncated: truncated, Servers: []string{p.SessionKey}}}, nil
}

// DocumentSymbols returns hierarchical symbols when supported by the server.
func (e *Engine) DocumentSymbols(ctx context.Context, in map[string]any) (map[string]any, error) {
	detail := stringVal(in, "detail")
	if detail != "" && detail != "outline" && detail != "compact" && detail != "full" {
		return nil, core.NewError(core.InvalidArgument, "detail must be compact, outline or full")
	}
	path := stringVal(in, "path")
	p, s, d, err := e.document(ctx, path, stringVal(in, "language"))
	if err != nil {
		return nil, err
	}
	raw, err := e.requestDocumentSymbolTree(ctx, p, s, d)
	if err != nil {
		return nil, err
	}
	tree, flat, hierarchical, err := decodeDocumentSymbolResponse(raw)
	if err != nil {
		return nil, err
	}
	if hierarchical {
		symbols, err := e.documentTree(p, s, d, tree)
		if err != nil {
			return nil, err
		}
		if detail == "full" {
			return map[string]any{"symbols": symbols}, nil
		}
		return map[string]any{"path": path, "symbols": outlineDocumentSymbols(symbols)}, nil
	}
	out, err := e.flatDocumentSymbols(p, s, d, flat)
	if err != nil {
		return nil, err
	}
	if detail == "full" {
		return map[string]any{"symbols": out}, nil
	}
	return map[string]any{"path": path, "symbols": outlineFlatSymbols(out)}, nil
}

// flatDocumentSymbols must not silently omit unresolvable LSP entries. An
// incomplete outline can mislead a coding agent about the declarations in a
// file, particularly when it is planning a multi-symbol refactor.
func (e *Engine) flatDocumentSymbols(p language.Profile, s *session.Session, d document.Document, flat []protocol.SymbolInformation) ([]core.SymbolSummary, error) {
	out := make([]core.SymbolSummary, 0, len(flat))
	for _, v := range flat {
		summary, err := e.symbolFromInfo(p, s, d, v)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "flat document symbol could not be resolved", err)
		}
		out = append(out, summary)
	}
	return out, nil
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
	view := stringVal(in, "relation_view")
	if view != "" && view != "locations" && view != "lines" {
		return nil, core.NewError(core.InvalidArgument, "relation_view must be locations or lines")
	}
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
	if view == "lines" {
		if err := e.populateLocationPreviews(locs); err != nil {
			return nil, err
		}
	}
	return map[string]any{"locations": locs, "meta": core.Meta{Complete: !tr, Truncated: tr}}, nil
}

// locationsAt runs one location-based LSP method against an already-resolved
// target without re-resolving or re-syncing the document.
func (e *Engine) locationsAt(ctx context.Context, c *fileCache, s *session.Session, uri string, pos protocol.Position, method string, extra map[string]any) ([]core.Location, error) {
	// A references query often returns hundreds of locations in one file.
	// Reuse source bytes for range conversion rather than rereading that file
	// for every result. Keep this cache request-local to avoid stale results.
	if c == nil {
		c = newFileCache()
	}
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
		// Some LSP servers support push diagnostics only. Probe the pull
		// method once, then avoid an identical failing LSP request on every
		// coding-agent diagnostic call. The cache is session-scoped and
		// invalidated on language-server restart.
		unsupported := s.PullDiagnosticsKnownUnsupported()
		var pullErr error
		if !unsupported {
			callCtx, cancel := e.callContext(ctx)
			pullErr = s.Request(callCtx, "textDocument/diagnostic", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "previousResultId": nil}, &raw)
			cancel()
			var protocolErr *transport.ProtocolError
			if errors.As(pullErr, &protocolErr) && protocolErr.Code == -32601 {
				s.MarkPullDiagnosticsUnsupported()
				unsupported = true
			}
		}
		diagnosticPath := relativeMust(e, d.Path)
		if pullErr == nil && !unsupported {
			ds, err = e.diagnostics(diagnosticPath, raw.Items, s.Capabilities().PositionEncoding)
			if err != nil {
				return nil, err
			}
		} else {
			// Some servers (including gopls versions in the real-LSP suite)
			// implement pull diagnostics without advertising diagnosticProvider.
			// Probe first; when an unadvertised server rejects the method, fall
			// back to its publishDiagnostics cache. Advertised pull failures remain
			// real errors and must not be hidden.
			if s.Capabilities().Diagnostics && !unsupported {
				return nil, pullErr
			}
			published, ready, err := s.WaitDiagnostics(ctx, d.URI, e.DiagnosticsWait)
			if err != nil {
				return nil, err
			}
			// A timeout without a publishDiagnostics event does not mean
			// the source is diagnostic-free. Signal that uncertainty to
			// the coding agent instead of presenting a false complete result.
			if !ready {
				complete = false
			}
			ds, err = e.diagnostics(diagnosticPath, published, s.Capabilities().PositionEncoding)
			if err != nil {
				return nil, err
			}
		}
	} else {
		// An unscoped diagnostic request cannot prove that the workspace is
		// error-free. Surface the remedy explicitly rather than returning a
		// misleading null/empty diagnostic list with no explanation.
		complete = false
	}
	ds = filterDiagnostics(ds, in)
	tr := len(ds) > limit
	if tr {
		ds = ds[:limit]
	}
	meta := core.Meta{Complete: complete && !tr, Truncated: tr}
	if path == "" {
		meta.Warnings = []string{"specify path to query current file diagnostics; an unscoped request cannot establish a clean result"}
	}
	return map[string]any{"diagnostics": ds, "meta": meta}, nil
}
