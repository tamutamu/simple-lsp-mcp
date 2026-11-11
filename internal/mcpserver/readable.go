package mcpserver

import (
	"fmt"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// renderReadableResult is presentation-only. It never derives semantic facts;
// it displays source and positions already returned by an LSP-backed tool.
func renderReadableResult(name string, input, data map[string]any) (string, bool) {
	// Readable is presentation-only. Never hide a failed/truncated LSP operation
	// or drop extra semantic relationships; use the complete JSON instead.
	if _, present := data["related"]; present {
		return "", false
	}
	if _, present := data["related_meta"]; present {
		return "", false
	}
	if rawMeta, present := data["meta"]; present {
		meta, ok := rawMeta.(core.Meta)
		if !ok || !meta.Complete || meta.Truncated || meta.SourceTruncated || len(meta.Warnings) > 0 {
			return "", false
		}
	}
	switch name {
	case "get_document_symbols":
		return renderReadableDocumentOutline(input, data)
	case "inspect_symbol":
		view, _ := input["view"].(string)
		if view == "location" || view == "signature" {
			return "", false // preserve selection ranges and symbol identity
		}
		return renderAgentSource(data)
	case "get_hover":
		return renderAgentHover(data)
	case "get_diagnostics":
		return renderReadableDiagnostics(input, data)
	case "preview_rename", "rename_symbol":
		return renderReadableRename(name, input, data)
	default:
		view, _ := input["relation_view"].(string)
		if view == "lines" || view == "source" {
			return renderAgentRelation(name, data)
		}
		return "", false
	}
}

// renderAgentSource turns escaped JSON source into directly readable code.
func renderAgentSource(data map[string]any) (string, bool) {
	items := []map[string]any{data}
	if raw, ok := data["results"]; ok {
		batch, ok := raw.([]map[string]any)
		if !ok {
			return "", false
		}
		items = batch
	}
	var out strings.Builder
	for i, item := range items {
		path, pathOK := item["path"].(string)
		name, nameOK := item["name"].(string)
		if !pathOK || !nameOK {
			return "", false
		}
		kind, _ := item["kind"].(string)
		if i > 0 {
			out.WriteByte('\n')
		}
		line, endLine := 0, 0
		if r, ok := item["range"].(core.Range); ok {
			line, endLine = r.Start.Line, r.End.Line
		}
		fmt.Fprintf(&out, "### %s:%d — %s (%s)\n", path, line, name, kind)
		if line > 0 && endLine >= line {
			fmt.Fprintf(&out, "Declaration: %s:%d-%d (from LSP; source may include context).\n", path, line, endLine)
		}
		if codeStart, ok := item["code_start_line"].(int); ok && codeStart > 0 {
			fmt.Fprintf(&out, "Source snippet starts at %s:%d.\n", path, codeStart)
		}
		if handle, ok := item["symbol_id"].(string); ok && handle != "" {
			fmt.Fprintf(&out, "Symbol handle: %s\n", handle)
		}
		if actual, ok := item["view"].(string); ok && actual == "edit" {
			out.WriteString("Use the declaration source below for the current snapshot; avoid fetching overlapping file text unless context is missing or the file was edited.\n")
		}
		for _, section := range []struct{ key, label string }{
			{"imports", "Relevant imports"},
			{"module_bindings", "Relevant module bindings"},
			{"code", "Declaration source"},
		} {
			source, _ := item[section.key].(string)
			if source == "" {
				continue
			}
			fmt.Fprintf(&out, "%s:\n%s", section.label, fencedSource(path, source))
		}
		if meta, ok := item["meta"].(core.Meta); ok && meta.Truncated {
			out.WriteString("WARNING: source truncated; inspect the remaining code before editing.\n")
		}
	}
	return out.String(), true
}

func fencedSource(path, source string) string {
	fence := "```"
	for strings.Contains(source, fence) {
		fence += "`"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s%s\n%s", fence, languageForPath(path), source)
	if !strings.HasSuffix(source, "\n") {
		out.WriteByte('\n')
	}
	fmt.Fprintf(&out, "%s\n", fence)
	return out.String()
}

func languageForPath(path string) string {
	switch {
	case strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
		return "typescript"
	case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".jsx"):
		return "javascript"
	default:
		return ""
	}
}
