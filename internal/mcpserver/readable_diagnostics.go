package mcpserver

import (
	"fmt"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// renderReadableDiagnostics preserves all diagnostic messages and positions
// while avoiding repeated JSON property names. Incomplete results and related
// locations remain JSON to avoid hiding correctness qualifications.
func renderReadableDiagnostics(input, data map[string]any) (string, bool) {
	if len(data) != 2 {
		return "", false
	}
	meta, ok := data["meta"].(core.Meta)
	if !ok || !meta.Complete || meta.Truncated || meta.SourceTruncated ||
		len(meta.Warnings) > 0 || len(meta.Servers) > 0 {
		return "", false
	}
	diagnostics, ok := data["diagnostics"].([]core.Diagnostic)
	if !ok {
		return "", false
	}
	path, _ := input["path"].(string)
	if path == "" {
		return "", false
	}
	var out strings.Builder
	if len(diagnostics) == 0 {
		return fmt.Sprintf("LSP diagnostics complete for %s: no diagnostics reported.\n", path), true
	}
	fmt.Fprintf(&out, "LSP diagnostics complete (%d) for %s:\n", len(diagnostics), path)
	for _, d := range diagnostics {
		if d.Path == "" || d.Range.Start.Line < 1 || d.Range.Start.Column < 1 ||
			d.Severity == "" || d.Message == "" || len(d.Locations) > 0 {
			return "", false
		}
		fmt.Fprintf(&out, "- %s:%d:%d [%s", d.Path, d.Range.Start.Line, d.Range.Start.Column, d.Severity)
		if d.Code != nil {
			fmt.Fprintf(&out, " %v", d.Code)
		}
		if d.Source != "" {
			fmt.Fprintf(&out, " / %s", d.Source)
		}
		fmt.Fprintf(&out, "]: %s\n", strings.ReplaceAll(d.Message, "\n", "\n  "))
	}
	return out.String(), true
}
