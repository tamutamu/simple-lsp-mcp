package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
)

const (
	defaultImpactDepth = 8
	maxImpactDepth = 16
	defaultImpactNodes = 80
	maxImpactNodes = 200
	maxImpactEdges = 512
)

type impactSymbol struct {
	Path string
	Node symbolNode
}

func (s impactSymbol) key() string {
	p := s.Node.SelectionRange.Start
	return fmt.Sprintf("%s:%d:%d:%s:%s", s.Path, p.Line, p.Column, s.Node.Kind, s.Node.Name)
}

type impactDocument struct {
	Profile language.Profile
	Session *session.Session
	Document document.Document
	Nodes []symbolNode
}

type impactReference struct {
	Owner impactSymbol
	Line int
}

type impactNode struct {
	ID string `json:"id"`
	Path string `json:"path"`
	Line int `json:"line"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Depth int `json:"depth"`
}

type impactEdge struct {
	From string `json:"from"`
	To string `json:"to"`
	ReferenceLines []int `json:"reference_lines"`
	ReferenceCount int `json:"reference_count"`
}

// AnalyzeImpact recursively follows exact LSP symbol references. Each reference
// is attributed to its enclosing declaration (prefer a function/method when
// inside one), which is then itself queried for references. This builds a
// breadth-first transitive impact graph, not an assertion that code is broken.
// A canonical declaration key is expanded at most once even in cyclic graphs.
func (e *Engine) AnalyzeImpact(ctx context.Context, in map[string]any) (map[string]any, error) {
	path, line := stringVal(in, "path"), intVal(in, "line")
	if path == "" || line < 1 {
		return nil, core.NewError(core.InvalidArgument, "path and a one-based declaration line are required")
	}
	depth := intVal(in, "depth")
	if depth == 0 {
		depth = defaultImpactDepth
	}
	if depth < 1 || depth > maxImpactDepth {
		return nil, core.NewError(core.InvalidArgument, "depth must be between 1 and 16")
	}
	nodeLimit := intVal(in, "max_nodes")
	if nodeLimit == 0 {
		nodeLimit = defaultImpactNodes
	}
	if nodeLimit < 1 || nodeLimit > maxImpactNodes {
		return nil, core.NewError(core.InvalidArgument, "max_nodes must be between 1 and 200")
	}
	profile, err := language.FromPath(path)
	if err != nil {
		return nil, err
	}
	server, doc, symbols, err := e.documentNodes(ctx, profile, path)
	if err != nil {
		return nil, err
	}
	selected, err := impactSymbolOnLine(symbols, line)
	if err != nil {
		return nil, err
	}
	root := impactSymbol{Path: relativeMust(e, doc.Path), Node: selected}
	initial := impactDocument{Profile: profile, Session: server, Document: doc, Nodes: symbols}
	docs := map[string]impactDocument{root.Path: initial}
	load := func(path string) (impactDocument, error) {
		if cached, ok := docs[path]; ok {
			return cached, nil
		}
		p, err := language.FromPath(path)
		if err != nil {
			return impactDocument{}, err
		}
		s, d, nodes, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return impactDocument{}, err
		}
		result := impactDocument{Profile: p, Session: s, Document: d, Nodes: nodes}
		docs[path] = result
		return result, nil
	}

	cache := newFileCache()
	fetch := func(ctx context.Context, current impactSymbol) ([]impactReference, int, error) {
		src, err := load(current.Path)
		if err != nil {
			return nil, 0, err
		}
		if !src.Session.Capabilities().References {
			return nil, 0, e.unsupported(src.Profile, "textDocument/references")
		}
		pos, err := document.ToLSP(src.Document.Text, current.Node.SelectionRange.Start, src.Session.Capabilities().PositionEncoding)
		if err != nil {
			return nil, 0, err
		}
		if err := e.warmProjectSymbolCandidates(ctx, src.Profile, src.Session, warmSymbolPathSpelling(current.Node.Name)); err != nil {
			return nil, 0, err
		}
		if err := e.primeProjectSemantics(ctx, src.Profile, src.Session, src.Document, pos); err != nil {
			return nil, 0, err
		}
		locations, err := e.locationsAt(ctx, cache, src.Session, src.Document.URI, pos,
			"textDocument/references", map[string]any{
				"context": map[string]bool{"includeDeclaration": false},
			})
		if err != nil {
			return nil, 0, err
		}
		sort.Slice(locations, func(i, j int) bool {
			a, b := locations[i], locations[j]
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			if a.Range.Start.Line != b.Range.Start.Line {
				return a.Range.Start.Line < b.Range.Start.Line
			}
			return a.Range.Start.Column < b.Range.Start.Column
		})
		refs := make([]impactReference, 0, len(locations))
		unmapped := 0
		for _, loc := range locations {
			ownerDoc, err := load(loc.Path)
			if err != nil {
				return nil, 0, core.WithCause(core.IncompleteSearch, "cannot resolve referenced file "+loc.Path, err)
			}
			owner, ok := impactEnclosingDeclaration(ownerDoc.Nodes, loc.Range.Start)
			if !ok {
				unmapped++
				continue
			}
			refs = append(refs, impactReference{
				Owner: impactSymbol{Path: loc.Path, Node: owner}, Line: loc.Range.Start.Line,
			})
		}
		return refs, unmapped, nil
	}
	nodes, edges, limits, unmapped, err := traverseImpact(ctx, root, depth, nodeLimit, maxImpactEdges, fetch)
	if err != nil {
		return nil, core.WithCause(core.IncompleteSearch, "LSP impact traversal failed", err)
	}
	complete := len(limits) == 0 && unmapped == 0
	return map[string]any{
		"root": nodes[0].ID, "nodes": nodes, "edges": edges,
		"depth": depth, "unmapped_references": unmapped,
		"meta": map[string]any{
			"complete": complete, "truncated": len(limits) > 0,
			"limit_reasons": limits,
			"note": "Static LSP references only: dynamic calls, reflection and unindexed sources may be missed. Edges indicate potential impact, not mandatory edits.",
		},
	}, nil
}

// traverseImpact is independent of protocol transport, making cycle, depth,
// count and edge de-duplication behavior directly testable.
func traverseImpact(ctx context.Context, root impactSymbol, depth, nodeLimit, edgeLimit int,
	fetch func(context.Context, impactSymbol) ([]impactReference, int, error)) ([]impactNode, []impactEdge, []string, int, error) {
	type work struct {
		symbol impactSymbol
		index int
		depth int
	}
	var nodes []impactNode
	var edges []impactEdge
	seen := map[string]int{}
	edgeIndices := map[string]int{}
	reasons := map[string]bool{}
	addNode := func(symbol impactSymbol, level int) int {
		index := len(nodes)
		id := fmt.Sprintf("n%d", index)
		nodes = append(nodes, impactNode{
			ID: id, Path: symbol.Path, Line: symbol.Node.SelectionRange.Start.Line,
			Name: symbol.Node.Name, Kind: symbol.Node.Kind, Depth: level,
		})
		seen[symbol.key()] = index
		return index
	}
	addNode(root, 0)
	queue := []work{{symbol: root, index: 0, depth: 0}}
	unmapped := 0
	for i := 0; i < len(queue); i++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, 0, err
		}
		item := queue[i]
		if item.depth >= depth {
			reasons["depth"] = true
			continue
		}
		refs, missing, err := fetch(ctx, item.symbol)
		if err != nil {
			return nil, nil, nil, 0, err
		}
		unmapped += missing
		for _, ref := range refs {
			key := ref.Owner.key()
			next, exists := seen[key]
			if !exists {
				if len(nodes) >= nodeLimit {
					reasons["max_nodes"] = true
					continue
				}
				next = addNode(ref.Owner, item.depth+1)
				queue = append(queue, work{symbol: ref.Owner, index: next, depth: item.depth+1})
			}
			from, to := nodes[item.index].ID, nodes[next].ID
			edgeKey := from+"->"+to
			edgeIndex, exists := edgeIndices[edgeKey]
			if !exists {
				if len(edges) >= edgeLimit {
					reasons["max_edges"] = true
					continue
				}
				edgeIndex = len(edges)
				edgeIndices[edgeKey] = edgeIndex
				edges = append(edges, impactEdge{From: from, To: to})
			}
			edge := &edges[edgeIndex]
			edge.ReferenceCount++
			if len(edge.ReferenceLines) < 8 {
				if count := len(edge.ReferenceLines); count == 0 || edge.ReferenceLines[count-1] != ref.Line {
					edge.ReferenceLines = append(edge.ReferenceLines, ref.Line)
				}
			}
		}
	}
	if len(nodes) == 1 && len(edges) == 0 {
		// A root with no outgoing impacts is fully explored even with depth=1.
		delete(reasons, "depth")
	}
	limits := make([]string, 0, len(reasons))
	for reason := range reasons {
		limits = append(limits, reason)
	}
	sort.Strings(limits)
	return nodes, edges, limits, unmapped, nil
}

// Selecting by a declaration line removes the need for the agent to locate a
// UTF-16/32 column. Never guess when multiple declarations share a line.
func impactSymbolOnLine(nodes []symbolNode, line int) (symbolNode, error) {
	var matches []symbolNode
	var walk func([]symbolNode)
	walk = func(xs []symbolNode) {
		for _, node := range xs {
			if node.SelectionRange.Start.Line == line {
				matches = append(matches, node)
			}
			walk(node.Children)
		}
	}
	walk(nodes)
	if len(matches) == 0 {
		return symbolNode{}, core.NewError(core.SymbolNotFound, "no declaration starts on the requested line")
	}
	if len(matches) != 1 {
		return symbolNode{}, core.NewError(core.AmbiguousSymbol, "multiple declarations start on this line; provide a line with one declaration")
	}
	return matches[0], nil
}

func impactEnclosingDeclaration(nodes []symbolNode, position core.Position) (symbolNode, bool) {
	var callable symbolNode
	var fallback symbolNode
	foundCall, foundFallback := false, false
	var walk func([]symbolNode)
	walk = func(xs []symbolNode) {
		for _, node := range xs {
			if !impactContains(node.Range, position) {
				continue
			}
			if impactCallable(node.Kind) {
				callable, foundCall = node, true
			}
			if impactTrackable(node.Kind) {
				fallback, foundFallback = node, true
			}
			walk(node.Children)
		}
	}
	walk(nodes)
	if foundCall {
		return callable, true
	}
	return fallback, foundFallback
}

func impactContains(r core.Range, p core.Position) bool {
	start, end := r.Start, r.End
	return (p.Line > start.Line || p.Line == start.Line && p.Column >= start.Column) &&
		(p.Line < end.Line || p.Line == end.Line && p.Column < end.Column)
}

func impactCallable(kind string) bool {
	switch kind {
	case "function", "method", "constructor":
		return true
	default:
		return false
	}
}

func impactTrackable(kind string) bool {
	if impactCallable(kind) {
		return true
	}
	switch kind {
	case "variable", "constant", "field", "property", "class", "interface", "struct", "module", "package", "type_parameter":
		return true
	default:
		return false
	}
}
