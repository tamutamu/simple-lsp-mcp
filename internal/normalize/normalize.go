package normalize

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func Position(p protocol.Position) core.Position {
	return core.Position{Line: p.Line + 1, Column: p.Character + 1}
}
func Range(r protocol.Range) core.Range {
	return core.Range{Start: Position(r.Start), End: Position(r.End)}
}
// FileURI encodes native absolute paths as canonical file URLs. In particular,
// Windows drive letters must be in the URL path (file:///C:/...), never in
// the authority (file://C:/...). Spaces, # and non-ASCII names are escaped.
func FileURI(path string) string {
	slash := filepath.ToSlash(path)
	if len(slash) >= 2 && isDriveLetter(slash[0]) && slash[1] == ':' {
		slash = "/" + slash
	}
	if strings.HasPrefix(slash, "//") {
		host, tail, ok := strings.Cut(strings.TrimPrefix(slash, "//"), "/")
		if ok && host != "" {
			return (&url.URL{Scheme: "file", Host: host, Path: "/" + tail}).String()
		}
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// URIPath decodes LSP file URLs without dropping a UNC host or treating a
// Windows drive as a URL authority. Remote file hosts on POSIX are rejected:
// ignoring their host would silently resolve a different local file.
func URIPath(ws *workspace.Workspace, uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", core.NewError(core.InvalidPath, "language server returned an invalid URI")
	}
	path := u.Path
	if u.Host != "" && u.Host != "localhost" {
		if runtime.GOOS != "windows" {
			return "", core.NewError(core.InvalidPath, "remote file URI host is not supported on this platform")
		}
		path = "//" + u.Host + path
	} else if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && isDriveLetter(path[1]) && path[2] == ':' {
		path = path[1:]
	}
	return ws.Relative(filepath.FromSlash(path))
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
