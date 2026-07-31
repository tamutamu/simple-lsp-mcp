package tools

import (
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

func (e *Engine) diagnostics(path string, xs []protocol.Diagnostic, encoding string) []core.Diagnostic {
	out := make([]core.Diagnostic, 0, len(xs))
	for _, x := range xs {
		r, err := e.rangeForPath(path, x.Range, encoding)
		if err != nil {
			continue
		}
		sev := 4
		if x.Severity != nil {
			sev = *x.Severity
		}
		d := core.Diagnostic{Path: path, Range: r, Severity: severity(sev), Code: x.Code, Source: x.Source, Message: x.Message}
		for _, ri := range x.RelatedInformation {
			l, err := e.location(ri.Location, encoding)
			if err == nil {
				d.Locations = append(d.Locations, l)
			}
		}
		out = append(out, d)
	}
	return out
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
