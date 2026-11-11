package mcpserver

import (
	"fmt"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// renderAgentHover presents language-server Markdown without JSON escaping.
func renderAgentHover(result map[string]any) (string, bool) {
	raw, ok := result["hover"]
	if !ok {
		return "", false
	}
	if raw == nil {
		return "No LSP hover/type information is available at this position.", true
	}
	hover, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	contents, ok := hover["contents"].(string)
	if !ok {
		return "", false
	}
	path, _ := hover["path"].(string)
	var out strings.Builder
	if r, ok := hover["range"].(core.Range); ok && r.Start.Line > 0 {
		if r.Start.Column > 0 {
			fmt.Fprintf(&out, "## LSP type information — %s:%d:%d\n\n", path, r.Start.Line, r.Start.Column)
		} else {
			fmt.Fprintf(&out, "## LSP type information — %s:%d\n\n", path, r.Start.Line)
		}
	} else if path != "" {
		fmt.Fprintf(&out, "## LSP type information — %s\n\n", path)
	} else {
		out.WriteString("## LSP type information\n\n")
	}
	out.WriteString(contents)
	if !strings.HasSuffix(contents, "\n") {
		out.WriteByte('\n')
	}
	if meta, ok := result["meta"].(core.Meta); ok && meta.SourceTruncated {
		out.WriteString("WARNING: LSP hover content was truncated.\n")
	}
	return out.String(), true
}
