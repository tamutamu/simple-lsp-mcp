package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
	"github.com/tamutamu/simple-lsp-mcp/internal/symbol"
)

// profileForInput resolves language explicitly when supplied, otherwise from
// a symbol handle or source path. symbol_path-only inputs are handled by the
// cross-language resolver because they have no file extension to inspect.
func (e *Engine) profileForInput(in map[string]any) (language.Profile, error) {
	if name := stringVal(in, "language"); name != "" {
		return language.Require(name)
	}
	if id := stringVal(in, "symbol_id"); id != "" {
		r, err := e.Symbols.Get(id)
		if err != nil {
			return language.Profile{}, err
		}
		return language.ForSessionKey(r.SessionKey, r.Path)
	}
	if path := stringVal(in, "path"); path != "" {
		return language.FromPath(path)
	}
	return language.Profile{}, core.NewError(core.InvalidArgument, "language could not be inferred; specify language or a source path")
}

// document resolves, starts, and synchronizes a file with its LSP session.
// languageName is optional when the path has a supported extension.
func (e *Engine) document(ctx context.Context, path, languageName string) (language.Profile, *session.Session, document.Document, error) {
	var (
		p   language.Profile
		err error
	)
	if languageName != "" {
		p, err = language.Require(languageName)
	} else {
		p, err = language.FromPath(path)
	}
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

// target resolves either a symbol handle, a symbol path, or a one-based
// source position. language is inferred whenever the target contains enough
// information to do so safely.
func (e *Engine) target(ctx context.Context, in map[string]any) (language.Profile, *session.Session, document.Document, protocol.Position, error) {
	t := targetOf(in)
	if err := t.Validate(); err != nil {
		return language.Profile{}, nil, document.Document{}, protocol.Position{}, err
	}

	if t.SymbolPath != "" {
		var (
			p    language.Profile
			hits []symbolLocation
			err  error
		)
		if stringVal(in, "language") == "" && t.Path == "" {
			hits, _, err = e.resolveSymbolPathAcrossLanguages(ctx, t.SymbolPath)
		} else {
			p, err = e.profileForInput(in)
			if err == nil {
				hits, _, err = e.resolveSymbolPath(ctx, p, t.SymbolPath, t.Path)
			}
		}
		if err != nil {
			return p, nil, document.Document{}, protocol.Position{}, err
		}
		switch len(hits) {
		case 0:
			return p, nil, document.Document{}, protocol.Position{}, core.NewError(core.SymbolNotFound, "no symbol matches symbol_path "+t.SymbolPath)
		case 1:
			hit := hits[0]
			p = hit.Profile
			pos, err := document.ToLSP(hit.Document.Text, hit.Node.SelectionRange.Start, hit.Session.Capabilities().PositionEncoding)
			return p, hit.Session, hit.Document, pos, err
		default:
			return p, nil, document.Document{}, protocol.Position{}, e.ambiguousError(t.SymbolPath, hits)
		}
	}

	p, err := e.profileForInput(in)
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
	path := relativeMust(e, d.Path)
	nodes := walkDocument(d.Text, s.Capabilities().PositionEncoding, vs, nil, "")
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, e.nodeMap(p, path, d.URI, d.Hash, n))
	}
	return out
}

// registerNode stores one walked node in the symbol registry and returns its handle.
func (e *Engine) registerNode(p language.Profile, path, uri, fileHash string, n symbolNode) string {
	r := symbol.Record{
		SessionKey:     p.SessionKey,
		Name:           n.Name,
		Kind:           n.Kind,
		ContainerName:  n.ContainerName,
		Path:           path,
		URI:            uri,
		FileHash:       fileHash,
		Range:          n.Range,
		SelectionRange: n.SelectionRange,
		SymbolPath:     n.SymbolPath,
		Detail:         n.Detail,
	}
	return e.Symbols.Register(r)
}

// nodeMap renders one walked node for tool output, registering it and recursing into children.
func (e *Engine) nodeMap(p language.Profile, path, uri, fileHash string, n symbolNode) map[string]any {
	id := e.registerNode(p, path, uri, fileHash, n)
	x := map[string]any{"symbol_id": id, "name": n.Name, "kind": n.Kind, "language": p.Name, "path": path, "range": n.Range, "selection_range": n.SelectionRange}
	if len(n.Children) > 0 {
		children := make([]any, 0, len(n.Children))
		for _, c := range n.Children {
			children = append(children, e.nodeMap(p, path, uri, fileHash, c))
		}
		x["children"] = children
	}
	return x
}

// locations accepts the three location response shapes permitted by LSP.
func (e *Engine) locations(raw json.RawMessage, encoding string) ([]core.Location, error) {
	return e.locationsIn(nil, raw, encoding)
}
func (e *Engine) locationsIn(c *fileCache, raw json.RawMessage, encoding string) ([]core.Location, error) {
	var links []protocol.LocationLink
	if json.Unmarshal(raw, &links) == nil && len(links) > 0 && links[0].TargetURI != "" {
		out := make([]core.Location, 0, len(links))
		for _, x := range links {
			l, err := e.linkIn(c, x, encoding)
			if err == nil {
				out = append(out, l)
			}
		}
		return out, nil
	}
	var one protocol.Location
	if json.Unmarshal(raw, &one) == nil && one.URI != "" {
		l, err := e.locationIn(c, one, encoding)
		return []core.Location{l}, err
	}
	var many []protocol.Location
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, err
	}
	out := make([]core.Location, 0, len(many))
	for _, x := range many {
		l, err := e.locationIn(c, x, encoding)
		if err == nil {
			out = append(out, l)
		}
	}
	return out, nil
}
func (e *Engine) location(l protocol.Location, encoding string) (core.Location, error) {
	return e.locationIn(nil, l, encoding)
}
func (e *Engine) locationIn(c *fileCache, l protocol.Location, encoding string) (core.Location, error) {
	path, err := normalize.URIPath(e.WS, l.URI)
	if err != nil {
		return core.Location{}, err
	}
	r, err := e.rangeIn(c, path, l.Range, encoding)
	if err != nil {
		return core.Location{}, err
	}
	return core.Location{Path: path, Range: r}, nil
}
func (e *Engine) link(l protocol.LocationLink, encoding string) (core.Location, error) {
	return e.linkIn(nil, l, encoding)
}
func (e *Engine) linkIn(c *fileCache, l protocol.LocationLink, encoding string) (core.Location, error) {
	path, err := normalize.URIPath(e.WS, l.TargetURI)
	if err != nil {
		return core.Location{}, err
	}
	r, err := e.rangeIn(c, path, l.TargetSelectionRange, encoding)
	if err != nil {
		return core.Location{}, err
	}
	return core.Location{Path: path, Range: r}, nil
}

// rangeFromText converts an LSP range using file content already in memory.
func rangeFromText(text []byte, r protocol.Range, encoding string) (core.Range, error) {
	a, err := document.FromLSP(text, r.Start, encoding)
	if err != nil {
		return core.Range{}, err
	}
	z, err := document.FromLSP(text, r.End, encoding)
	return core.Range{Start: a, End: z}, err
}
func (e *Engine) rangeForPath(path string, r protocol.Range, encoding string) (core.Range, error) {
	return e.rangeIn(nil, path, r, encoding)
}
func (e *Engine) rangeIn(c *fileCache, path string, r protocol.Range, encoding string) (core.Range, error) {
	b, err := c.read(e.WS, path)
	if err != nil {
		return core.Range{}, err
	}
	return rangeFromText(b, r, encoding)
}
func (e *Engine) fileHash(path string) string {
	return e.hashIn(nil, path)
}
func (e *Engine) hashIn(c *fileCache, path string) string {
	return c.hashOf(e.WS, path)
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
	case "hover":
		return c.Hover
	case "documentSymbol":
		return c.DocumentSymbol
	case "callHierarchy":
		return c.CallHierarchy
	case "typeHierarchy":
		return c.TypeHierarchy
	case "workspaceSymbol":
		return c.WorkspaceSymbol
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
