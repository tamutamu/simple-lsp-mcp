package tools

import (
	"strconv"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

// symbolNode is one document symbol carrying its resolved symbol path and
// one-based ranges. It has no dependency on an LSP session or the symbol
// registry, so it can be built and tested without either.
type symbolNode struct {
	Name           string
	Detail         string
	Kind           string
	Segments       []string // raw (unescaped), outermost ancestor first
	SymbolPath     string   // escaped and joined, disambiguated among siblings
	ContainerName  string   // the immediate parent's raw name, or "" at the root
	Range          core.Range
	SelectionRange core.Range
	Children       []symbolNode
}

const symbolPathSeparator = "/"

// escapeSegment percent-encodes the two characters that would otherwise be
// ambiguous inside a symbol path: "%" and the separator itself.
func escapeSegment(name string) string {
	return strings.NewReplacer("%", "%25", "/", "%2F").Replace(name)
}

// unescapeSegment reverses escapeSegment.
func unescapeSegment(segment string) string {
	return strings.NewReplacer("%2F", "/", "%25", "%").Replace(segment)
}

// joinSymbolPath renders a full symbol path from its raw (unescaped) segments.
func joinSymbolPath(segments []string) string {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = escapeSegment(s)
	}
	return strings.Join(escaped, symbolPathSeparator)
}

// siblingSegments assigns each document symbol in one sibling group a raw
// path segment: the symbol's own name, disambiguated when a name repeats
// ("createUser", "createUser#2", "createUser#3", ...), or "#<index>" when
// the symbol has no name at all.
func siblingSegments(vs []protocol.DocumentSymbol) []string {
	segments := make([]string, len(vs))
	counts := map[string]int{}
	for i, v := range vs {
		if v.Name == "" {
			segments[i] = "#" + strconv.Itoa(i)
			continue
		}
		counts[v.Name]++
		if counts[v.Name] == 1 {
			segments[i] = v.Name
		} else {
			segments[i] = v.Name + "#" + strconv.Itoa(counts[v.Name])
		}
	}
	return segments
}

// walkDocument converts an LSP document symbol tree into nodes carrying
// resolved symbol paths. text is the file content already held by the
// document store, so no file is read here. parents holds the raw
// (unescaped, already disambiguated) path segments of every enclosing
// symbol; containerName is the immediate parent's raw name, or "" at the
// document root.
func walkDocument(text []byte, encoding string, vs []protocol.DocumentSymbol, parents []string, containerName string) []symbolNode {
	segs := siblingSegments(vs)
	out := make([]symbolNode, 0, len(vs))
	for i, v := range vs {
		rr, err := rangeFromText(text, v.Range, encoding)
		if err != nil {
			continue
		}
		sr, err := rangeFromText(text, v.SelectionRange, encoding)
		if err != nil {
			continue
		}
		segments := make([]string, len(parents)+1)
		copy(segments, parents)
		segments[len(parents)] = segs[i]
		node := symbolNode{
			Name:           v.Name,
			Detail:         v.Detail,
			Kind:           normalize.Kind(v.Kind),
			Segments:       segments,
			SymbolPath:     joinSymbolPath(segments),
			ContainerName:  containerName,
			Range:          rr,
			SelectionRange: sr,
		}
		if len(v.Children) > 0 {
			node.Children = walkDocument(text, encoding, v.Children, segments, v.Name)
		}
		out = append(out, node)
	}
	return out
}
