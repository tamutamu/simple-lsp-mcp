package tools

import (
	"context"
	"encoding/json"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
	"github.com/tamutamu/simple-lsp-mcp/internal/symbol"
)

func (e *Engine) callHierarchy(ctx context.Context, name string, p language.Profile, s *session.Session, raw json.RawMessage, in map[string]any) (map[string]any, error) {
	var items []protocol.CallHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, core.NewError(core.SymbolNotFound, "call hierarchy item was not found")
	}
	method := "callHierarchy/incomingCalls"
	if name == "get_outgoing_calls" {
		method = "callHierarchy/outgoingCalls"
	}
	var response json.RawMessage
	c, cancel := e.callContext(ctx)
	err := s.Request(c, method, map[string]any{"item": items[0]}, &response)
	cancel()
	if err != nil {
		return nil, err
	}
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	var out []any
	if name == "get_incoming_calls" {
		var xs []protocol.CallHierarchyIncomingCall
		if err = json.Unmarshal(response, &xs); err != nil {
			return nil, err
		}
		for _, x := range xs {
			out = append(out, e.callItem(p, s, x.From, x.FromRanges))
		}
	} else {
		var xs []protocol.CallHierarchyOutgoingCall
		if err = json.Unmarshal(response, &xs); err != nil {
			return nil, err
		}
		for _, x := range xs {
			out = append(out, e.callItem(p, s, x.To, x.FromRanges))
		}
	}
	tr := len(out) > limit
	if tr {
		out = out[:limit]
	}
	return map[string]any{"calls": out, "meta": core.Meta{Complete: true, Truncated: tr}}, nil
}
func (e *Engine) callItem(p language.Profile, s *session.Session, x protocol.CallHierarchyItem, from []protocol.Range) map[string]any {
	path, err := normalize.URIPath(e.WS, x.URI)
	if err != nil {
		return map[string]any{"name": x.Name}
	}
	r, err := e.rangeForPath(path, x.Range, s.Capabilities().PositionEncoding)
	if err != nil {
		return map[string]any{"name": x.Name}
	}
	sr, err := e.rangeForPath(path, x.SelectionRange, s.Capabilities().PositionEncoding)
	if err != nil {
		sr = r
	}
	id := e.Symbols.Register(symbol.Record{SessionKey: p.SessionKey, Name: x.Name, Kind: normalize.Kind(x.Kind), Path: path, URI: x.URI, Range: r, SelectionRange: sr, FileHash: e.fileHash(path), Data: x.Data})
	return map[string]any{"symbol_id": id, "name": x.Name, "kind": normalize.Kind(x.Kind), "path": path, "range": r, "selection_range": sr, "from_ranges": from}
}
func (e *Engine) typeHierarchy(ctx context.Context, name string, p language.Profile, s *session.Session, raw json.RawMessage, in map[string]any) (map[string]any, error) {
	var items []protocol.TypeHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, core.NewError(core.SymbolNotFound, "type hierarchy item was not found")
	}
	method := "typeHierarchy/supertypes"
	if name == "get_subtypes" {
		method = "typeHierarchy/subtypes"
	}
	var xs []protocol.TypeHierarchyItem
	c, cancel := e.callContext(ctx)
	err := s.Request(c, method, map[string]any{"item": items[0]}, &xs)
	cancel()
	if err != nil {
		return nil, err
	}
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(xs))
	for _, x := range xs {
		out = append(out, e.typeItem(p, s, x))
	}
	tr := len(out) > limit
	if tr {
		out = out[:limit]
	}
	return map[string]any{"types": out, "meta": core.Meta{Complete: true, Truncated: tr}}, nil
}
func (e *Engine) typeItem(p language.Profile, s *session.Session, x protocol.TypeHierarchyItem) map[string]any {
	path, err := normalize.URIPath(e.WS, x.URI)
	if err != nil {
		return map[string]any{"name": x.Name}
	}
	r, err := e.rangeForPath(path, x.Range, s.Capabilities().PositionEncoding)
	if err != nil {
		return map[string]any{"name": x.Name}
	}
	sr, err := e.rangeForPath(path, x.SelectionRange, s.Capabilities().PositionEncoding)
	if err != nil {
		sr = r
	}
	id := e.Symbols.Register(symbol.Record{SessionKey: p.SessionKey, Name: x.Name, Kind: normalize.Kind(x.Kind), Path: path, URI: x.URI, Range: r, SelectionRange: sr, FileHash: e.fileHash(path), Data: x.Data})
	return map[string]any{"symbol_id": id, "name": x.Name, "kind": normalize.Kind(x.Kind), "path": path, "range": r, "selection_range": sr}
}
