package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// readableRenameFile is a presentation-only view of the already validated
// LSP WorkspaceEdit. The full response is unchanged in both/text modes.
type readableRenameFile struct {
	Path        string            `json:"path"`
	Edits       []json.RawMessage `json:"edits"`
	EditCount   int               `json:"edit_count"`
	SampleEdits []struct {
		Range   core.Range `json:"range"`
		NewText string     `json:"new_text"`
	} `json:"sample_edits"`
	OmittedEdits int `json:"omitted_edits"`
}

func renderReadableRename(name string, input, data map[string]any) (string, bool) {
	applied, ok := data["applied"].(bool)
	if !ok {
		return "", false
	}
	summary := false
	switch name {
	case "rename_symbol":
		if !applied || input["apply"] != true {
			return "", false // a non-applied rename needs its exact preview
		}
	case "preview_rename":
		if applied || input["preview_detail"] == "full" ||
			data["preview_detail"] != "summary" ||
			data["preview_truncated"] != true {
			return "", false // keep explicitly requested full edits intact
		}
		summary = true
	default:
		return "", false
	}
	// Do not silently discard future server fields or warnings.
	allowed := map[string]bool{
		"applied": true, "new_name": true, "file_count": true,
		"edit_count": true, "files": true, "prepare": true,
		"warnings": true, "preview_detail": true, "preview_truncated": true,
		"sample_edit_count": true, "full_preview_available": true,
	}
	for key := range data {
		if !allowed[key] {
			return "", false
		}
	}
	if warnings, present := data["warnings"]; present && warnings != nil {
		b, err := json.Marshal(warnings)
		if err != nil || (string(b) != "[]" && string(b) != "null") {
			return "", false
		}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return "", false
	}
	var v struct {
		Applied       bool                 `json:"applied"`
		NewName       string               `json:"new_name"`
		FileCount     int                  `json:"file_count"`
		EditCount     int                  `json:"edit_count"`
		Files         []readableRenameFile `json:"files"`
		Prepare       json.RawMessage      `json:"prepare"`
		PreviewDetail string               `json:"preview_detail"`
	}
	if err := json.Unmarshal(b, &v); err != nil ||
		strings.TrimSpace(v.NewName) == "" ||
		v.FileCount <= 0 || v.EditCount <= 0 ||
		v.FileCount != len(v.Files) || v.Applied != applied {
		return "", false
	}
	total := 0
	var out strings.Builder
	if summary {
		fmt.Fprintf(&out, "READ ONLY — LSP semantic rename preview to %q: %d edits across %d files. Nothing has been changed.\n", v.NewName, v.EditCount, v.FileCount)
	} else {
		fmt.Fprintf(&out, "APPLIED — LSP semantic rename to %q: %d edits across %d files. Files have been changed; run the relevant tests.\n", v.NewName, v.EditCount, v.FileCount)
	}
	if path, ok := input["path"].(string); ok && path != "" {
		if symbol, ok := input["symbol_path"].(string); ok && symbol != "" {
			fmt.Fprintf(&out, "Target: %s :: %s\n", path, symbol)
		}
	}
	for _, file := range v.Files {
		if file.Path == "" {
			return "", false
		}
		number := len(file.Edits)
		if summary {
			number = file.EditCount
			if number <= 0 || file.OmittedEdits < 0 ||
				number < file.OmittedEdits+len(file.SampleEdits) {
				return "", false
			}
		} else if len(file.Edits) == 0 {
			return "", false
		}
		total += number
		suffix := "s"
		if number == 1 {
			suffix = ""
		}
		fmt.Fprintf(&out, "- %s: %d edit%s", file.Path, number, suffix)
		if summary && len(file.SampleEdits) > 0 {
			out.WriteString("; examples ")
			for i, edit := range file.SampleEdits {
				if i > 0 {
					out.WriteString(", ")
				}
				if edit.Range.Start.Line < 1 {
					return "", false
				}
				fmt.Fprintf(&out, "%d:%d→%q", edit.Range.Start.Line, edit.Range.Start.Column, edit.NewText)
			}
		}
		if summary && file.OmittedEdits > 0 {
			fmt.Fprintf(&out, " (%d other edits not shown)", file.OmittedEdits)
		}
		out.WriteByte('\n')
	}
	if total != v.EditCount {
		return "", false
	}
	if len(v.Prepare) > 0 && string(v.Prepare) != "null" {
		fmt.Fprintf(&out, "LSP prepareRename context: %s\n", v.Prepare)
	}
	if summary {
		out.WriteString("For exact edit ranges, explicitly request preview_rename(preview_detail=full). Apply only when the file scope is correct and writes are authorized.\n")
	}
	return out.String(), true
}
