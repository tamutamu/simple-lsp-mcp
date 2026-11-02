package tools

import (
	"fmt"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

func (e *Engine) diagnostics(path string, xs []protocol.Diagnostic, encoding string) ([]core.Diagnostic, error) {
	// A diagnostic batch can contain hundreds of locations in the same file.
	// Reuse the source and its position index rather than rereading the file
	// for every primary or related range.
	cache := newFileCache()
	out := make([]core.Diagnostic, 0, len(xs))
	for i, x := range xs {
		r, err := e.rangeIn(cache, path, x.Range, encoding)
		if err != nil {
			// Silently skipping an invalid primary range can turn a file with
			// errors into a misleading clean diagnostic result.
			return nil, core.WithCause(core.IncompleteSearch,
				fmt.Sprintf("invalid LSP diagnostic range in %s at item %d", path, i+1), err)
		}
		sev := 4
		if x.Severity != nil {
			sev = *x.Severity
		}
		d := core.Diagnostic{Path: path, Range: r, Severity: severity(sev), Code: x.Code, Source: x.Source, Message: x.Message}
		for _, ri := range x.RelatedInformation {
			l, err := e.locationIn(cache, ri.Location, encoding)
			if err == nil {
				d.Locations = append(d.Locations, l)
			}
		}
		out = append(out, d)
	}
	return out, nil
}
func severity(n int) string {
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
func filterDiagnostics(xs []core.Diagnostic, in map[string]any) []core.Diagnostic {
	v, ok := in["severities"].([]any)
	if !ok || len(v) == 0 {
		return xs
	}
	allow := map[string]bool{}
	for _, x := range v {
		if s, ok := x.(string); ok {
			allow[s] = true
		}
	}
	out := xs[:0]
	for _, x := range xs {
		if allow[x.Severity] {
			out = append(out, x)
		}
	}
	return out
}
