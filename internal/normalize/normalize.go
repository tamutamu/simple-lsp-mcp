package normalize

import (
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
	"net/url"
	"path/filepath"
	"strings"
)

func Position(p protocol.Position) core.Position {
	return core.Position{Line: p.Line + 1, Column: p.Character + 1}
}
func Range(r protocol.Range) core.Range {
	return core.Range{Start: Position(r.Start), End: Position(r.End)}
}
func URIPath(ws *workspace.Workspace, uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", core.NewError(core.InvalidPath, "language server returned an invalid URI")
	}
	return ws.Relative(filepath.FromSlash(u.Path))
}
func Location(ws *workspace.Workspace, l protocol.Location) (core.Location, error) {
	p, err := URIPath(ws, l.URI)
	if err != nil {
		return core.Location{}, err
	}
	return core.Location{Path: p, Range: Range(l.Range)}, nil
}
func Link(ws *workspace.Workspace, l protocol.LocationLink) (core.Location, error) {
	p, err := URIPath(ws, l.TargetURI)
	if err != nil {
		return core.Location{}, err
	}
	return core.Location{Path: p, Range: Range(l.TargetSelectionRange)}, nil
}
func Kind(k int) string {
	if n, ok := kinds[k]; ok {
		return n
	}
	return "unknown"
}

var kinds = map[int]string{1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class", 6: "method", 7: "property", 8: "field", 9: "constructor", 10: "enum", 11: "interface", 12: "function", 13: "variable", 14: "constant", 15: "string", 16: "number", 17: "boolean", 18: "array", 19: "object", 20: "key", 21: "null", 22: "enum_member", 23: "struct", 24: "event", 25: "operator", 26: "type_parameter"}

func Severity(n int) string {
	switch n {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "information"
	default:
		return "hint"
	}
}
func Preview(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 240 {
		return line[:240]
	}
	return line
}
