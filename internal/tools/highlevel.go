package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

// maxProbeFiles bounds how many candidate files a workspace-wide
// symbol_path search will open when no path narrows the search to one file.
const maxProbeFiles = 8

// symbolLocation is one resolved symbol_path hit, holding everything needed
// to target it exactly like a symbol_id or a path+line+column would be.
type symbolLocation struct {
	Session  *session.Session
	Document document.Document
	Node     symbolNode
}

// documentNodes fetches and walks the document symbol tree of one file. A
// flat SymbolInformation[] response (no parent/child structure) becomes a
// set of single-segment root-level nodes; matchNodes can still find them by
// name, just without any nesting.
func (e *Engine) documentNodes(ctx context.Context, p language.Profile, path string) (*session.Session, document.Document, []symbolNode, error) {
	_, s, d, err := e.document(ctx, path, p.Name)
	if err != nil {
		return nil, document.Document{}, nil, err
	}
	raw, err := e.requestDocumentSymbolTree(ctx, p, s, d)
	if err != nil {
		return nil, document.Document{}, nil, err
	}
	var tree []protocol.DocumentSymbol
	if json.Unmarshal(raw, &tree) == nil && tree != nil {
		return s, d, walkDocument(d.Text, s.Capabilities().PositionEncoding, tree, nil, ""), nil
	}
	var flat []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, document.Document{}, nil, err
	}
	nodes := make([]symbolNode, 0, len(flat))
	for _, v := range flat {
		rr, err := rangeFromText(d.Text, v.Location.Range, s.Capabilities().PositionEncoding)
		if err != nil {
			continue
		}
		segments := []string{v.Name}
		nodes = append(nodes, symbolNode{
			Name: v.Name, Kind: normalize.Kind(v.Kind),
			Segments: segments, SymbolPath: joinSymbolPath(segments),
			ContainerName: v.ContainerName, Range: rr, SelectionRange: rr,
		})
	}
	return s, d, nodes, nil
}

// candidateFiles asks the language server's workspace symbol index which
// files might contain a symbol path's leaf name. Results are not
// registered in the symbol registry: most of them will never be used, and
// registering unused candidates would only grow the registry for nothing.
func (e *Engine) candidateFiles(ctx context.Context, p language.Profile, leaf string) ([]string, error) {
	s, err := e.Sessions.For(p.SessionKey)
	if err != nil {
		return nil, err
	}
	var raw []protocol.WorkspaceSymbol
	callCtx, cancel := e.callContext(ctx)
	err = s.Request(callCtx, "workspace/symbol", map[string]string{"query": leaf}, &raw)
	cancel()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, v := range raw {
		path, err := normalize.URIPath(e.WS, v.Location.URI)
		if err != nil || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	return files, nil
}

// resolveSymbolPath finds every symbol matching symbolPath. When path is
// set, it searches only that file (one LSP request). Otherwise it asks
// workspace/symbol for candidate files by the path's leaf name and
// searches each of them, up to maxProbeFiles.
func (e *Engine) resolveSymbolPath(ctx context.Context, p language.Profile, symbolPath, path string) ([]symbolLocation, []string, error) {
	segments, anchored, err := parseSymbolPath(symbolPath)
	if err != nil {
		return nil, nil, err
	}
	if path != "" {
		s, d, nodes, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return nil, nil, err
		}
		return locationsFor(s, d, matchNodes(nodes, segments, anchored)), nil, nil
	}
	leaf := segments[len(segments)-1]
	files, err := e.candidateFiles(ctx, p, leaf)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if len(files) > maxProbeFiles {
		warnings = append(warnings, fmt.Sprintf("more than %d files match %q; pass path to narrow the search", maxProbeFiles, leaf))
		files = files[:maxProbeFiles]
	}
	var hits []symbolLocation
	for _, f := range files {
		s, d, nodes, err := e.documentNodes(ctx, p, f)
		if err != nil {
			continue
		}
		hits = append(hits, locationsFor(s, d, matchNodes(nodes, segments, anchored))...)
	}
	return hits, warnings, nil
}

func locationsFor(s *session.Session, d document.Document, nodes []symbolNode) []symbolLocation {
	out := make([]symbolLocation, len(nodes))
	for i, n := range nodes {
		out[i] = symbolLocation{Session: s, Document: d, Node: n}
	}
	return out
}

// ambiguousError renders an AMBIGUOUS_SYMBOL error listing up to five
// candidates inline, for tools whose output shape cannot carry a
// structured candidate list.
func (e *Engine) ambiguousError(symbolPath string, hits []symbolLocation) error {
	const maxInline = 5
	names := make([]string, 0, len(hits))
	for i, h := range hits {
		if i >= maxInline {
			names = append(names, fmt.Sprintf("and %d more", len(hits)-maxInline))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s)", h.Node.SymbolPath, relativeMust(e, h.Document.Path)))
	}
	return core.NewError(core.AmbiguousSymbol, fmt.Sprintf("%q matches %d symbols: %s", symbolPath, len(hits), strings.Join(names, ", ")))
}
