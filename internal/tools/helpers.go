package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
	"github.com/tamutamu/simple-lsp-mcp/internal/symbol"
)

// document resolves, starts, and synchronizes a file with its LSP session.
func (e *Engine) document(ctx context.Context, path, languageName string) (language.Profile, *session.Session, document.Document, error) {
	p, err := language.Require(languageName)
	if err != nil {
		return language.Profile{}, nil, document.Document{}, err
	}
	full, err := e.WS.Resolve(path)
	if err != nil {
		return p, nil, document.Document{}, err
	}
	s, err := e.Sessions.ForPath(p.SessionKey, path)
	if err != nil {
		return p, nil, document.Document{}, err
	}
	if err := s.Ensure(ctx); err != nil {
		return p, nil, document.Document{}, err
	}
	d, err := e.Docs.Sync(ctx, s, full, p.LanguageID)
	return p, s, d, err
}

// target resolves either a symbol handle or a one-based source position.
func (e *Engine) target(ctx context.Context, in map[string]any) (language.Profile, *session.Session, document.Document, protocol.Position, error) {
	t := targetOf(in)
	if err := t.Validate(); err != nil {
		return language.Profile{}, nil, document.Document{}, protocol.Position{}, err
	}
	p, err := language.Require(stringVal(in, "language"))
	if err != nil {
		return language.Profile{}, nil, document.Document{}, protocol.Position{}, err
	}
	if t.SymbolID != "" {
		r, err := e.Symbols.Get(t.SymbolID)
		if err != nil {
			return language.Profile{}, nil, document.Document{}, protocol.Position{}, err
		}
		if p.SessionKey != r.SessionKey {
			return p, nil, document.Document{}, protocol.Position{}, core.NewError(core.InvalidArgument, "language does not match symbol")
		}
		s, err := e.Sessions.ForPath(r.SessionKey, r.Path)

		if err != nil {
			return p, nil, document.Document{}, protocol.Position{}, err
		}
		if err := s.Ensure(ctx); err != nil {
			return p, nil, document.Document{}, protocol.Position{}, err
		}
		full, err := e.WS.Resolve(r.Path)
		if err != nil {
			return p, nil, document.Document{}, protocol.Position{}, err
		}
		d, err := e.Docs.Sync(ctx, s, full, p.LanguageID)
		if err != nil {
			return p, nil, document.Document{}, protocol.Position{}, err
		}
		if hash(d.Text) != r.FileHash {
			return p, nil, document.Document{}, protocol.Position{}, core.NewError(core.StaleSymbol, "symbol changed; search again")
		}
		pos, err := document.ToLSP(d.Text, r.SelectionRange.Start, s.Capabilities().PositionEncoding)
		return p, s, d, pos, err
	}
	p, s, d, err := e.document(ctx, t.Path, stringVal(in, "language"))
	if err != nil {
		return p, s, d, protocol.Position{}, err
	}
	pos, err := document.ToLSP(d.Text, core.Position{Line: t.Line, Column: t.Column}, s.Capabilities().PositionEncoding)
	return p, s, d, pos, err
}

// symbolFromWorkspace registers an LSP workspace-symbol response for later use.
func (e *Engine) symbolFromWorkspace(p language.Profile, v protocol.WorkspaceSymbol, s *session.Session) (core.SymbolSummary, error) {
	l, err := e.location(protocol.Location{URI: v.Location.URI, Range: v.Location.Range}, s.Capabilities().PositionEncoding)
	if err != nil {
		return core.SymbolSummary{}, err
	}
	r := symbol.Record{SessionKey: p.SessionKey, Name: v.Name, Kind: normalize.Kind(v.Kind), ContainerName: v.ContainerName, Path: l.Path, URI: v.Location.URI, Range: l.Range, SelectionRange: l.Range, FileHash: e.fileHash(l.Path)}
	id := e.Symbols.Register(r)
	return core.SymbolSummary{SymbolID: id, Name: v.Name, Kind: r.Kind, ContainerName: v.ContainerName, Language: p.Name, Path: l.Path, Range: l.Range, SelectionRange: l.Range}, nil
}

// symbolFromInfo registers a flat document-symbol response for later use.
func (e *Engine) symbolFromInfo(p language.Profile, s *session.Session, d document.Document, v protocol.SymbolInformation) (core.SymbolSummary, error) {
	l, err := e.location(v.Location, s.Capabilities().PositionEncoding)
	if err != nil {
		return core.SymbolSummary{}, err
	}
	r := symbol.Record{SessionKey: p.SessionKey, Name: v.Name, Kind: normalize.Kind(v.Kind), ContainerName: v.ContainerName, Path: l.Path, URI: v.Location.URI, Range: l.Range, SelectionRange: l.Range, FileHash: e.fileHash(l.Path)}
	id := e.Symbols.Register(r)
	return core.SymbolSummary{SymbolID: id, Name: v.Name, Kind: r.Kind, ContainerName: v.ContainerName, Language: p.Name, Path: l.Path, Range: l.Range, SelectionRange: l.Range}, nil
}

// documentTree converts and registers recursive document symbols.
func (e *Engine) documentTree(p language.Profile, s *session.Session, d document.Document, vs []protocol.DocumentSymbol) []any {
	out := make([]any, 0, len(vs))
	path := relativeMust(e, d.Path)
	for _, v := range vs {
		rr, err := e.rangeForPath(path, v.Range, s.Capabilities().PositionEncoding)
		if err != nil {
			continue
		}
		sr, err := e.rangeForPath(path, v.SelectionRange, s.Capabilities().PositionEncoding)
		if err != nil {
			continue
		}
		r := symbol.Record{SessionKey: p.SessionKey, Name: v.Name, Kind: normalize.Kind(v.Kind), Path: path, URI: d.URI, Range: rr, SelectionRange: sr, FileHash: d.Hash}
		id := e.Symbols.Register(r)
		x := map[string]any{"symbol_id": id, "name": v.Name, "kind": r.Kind, "language": p.Name, "path": r.Path, "range": rr, "selection_range": sr}
		if len(v.Children) > 0 {
			x["children"] = e.documentTree(p, s, d, v.Children)
		}
		out = append(out, x)
	}
	return out
}

// locations accepts the three location response shapes permitted by LSP.
func (e *Engine) locations(raw json.RawMessage, encoding string) ([]core.Location, error) {
	var links []protocol.LocationLink
	if json.Unmarshal(raw, &links) == nil && len(links) > 0 && links[0].TargetURI != "" {
		out := make([]core.Location, 0, len(links))
		for _, x := range links {
			l, err := e.link(x, encoding)
			if err == nil {
				out = append(out, l)
			}
		}
		return out, nil
	}
	var one protocol.Location
	if json.Unmarshal(raw, &one) == nil && one.URI != "" {
		l, err := e.location(one, encoding)
		return []core.Location{l}, err
	}
	var many []protocol.Location
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, err
	}
	out := make([]core.Location, 0, len(many))
	for _, x := range many {
		l, err := e.location(x, encoding)
		if err == nil {
			out = append(out, l)
		}
	}
	return out, nil
}
func (e *Engine) location(l protocol.Location, encoding string) (core.Location, error) {
	path, err := normalize.URIPath(e.WS, l.URI)
	if err != nil {
		return core.Location{}, err
	}
	r, err := e.rangeForPath(path, l.Range, encoding)
	if err != nil {
		return core.Location{}, err
	}
	return core.Location{Path: path, Range: r}, nil
}
func (e *Engine) link(l protocol.LocationLink, encoding string) (core.Location, error) {
	path, err := normalize.URIPath(e.WS, l.TargetURI)
	if err != nil {
		return core.Location{}, err
	}
	r, err := e.rangeForPath(path, l.TargetSelectionRange, encoding)
	if err != nil {
		return core.Location{}, err
	}
	return core.Location{Path: path, Range: r}, nil
}
func (e *Engine) rangeForPath(path string, r protocol.Range, encoding string) (core.Range, error) {
	full, err := e.WS.Resolve(path)
	if err != nil {
		return core.Range{}, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return core.Range{}, err
	}
	a, err := document.FromLSP(b, r.Start, encoding)
	if err != nil {
		return core.Range{}, err
	}
	z, err := document.FromLSP(b, r.End, encoding)
	return core.Range{Start: a, End: z}, err
}
func (e *Engine) fileHash(path string) string {
	full, err := e.WS.Resolve(path)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return ""
	}
	return hash(b)
}
func hash(b []byte) string { x := sha256.Sum256(b); return hex.EncodeToString(x[:]) }
func relativeMust(e *Engine, path string) string {
	x, err := e.WS.Relative(path)
	if err != nil {
		return path
	}
	return x
}
func (e *Engine) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, e.Timeout)
}
func (e *Engine) limit(in map[string]any) (int, error) {
	return core.ClampLimit(intVal(in, "limit"), e.MaxResults, e.MaxResults)
}
func (e *Engine) unsupported(p language.Profile, method string) error {
	x := core.NewError(core.MethodNotSupported, p.Name+" does not advertise "+method)
	x.Language = p.Name
	x.Method = method
	return x
}
func capable(c protocol.Capabilities, name string) bool {
	switch name {
	case "definition":
		return c.Definition
	case "references":
		return c.References
	case "implementation":
		return c.Implementation
	case "typeDefinition":
		return c.TypeDefinition
	case "declaration":
		return c.Declaration
	}
	return false
}
func languageName(key string) string {
	if key == "typescript-javascript" {
		return "typescript"
	}
	return key
}
func sliceRange(b []byte, r core.Range) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	if r.Start.Line < 1 || r.Start.Line > len(lines) {
		return nil
	}
	end := r.End.Line
	if end > len(lines) {
		end = len(lines)
	}
	return []byte(strings.Join(lines[r.Start.Line-1:end], ""))
}
