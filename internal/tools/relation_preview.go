package tools

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// populateLocationPreviews adds the actual source line to exact LSP locations.
// This lets agents request rg-like evidence without another file read while
// preserving the semantic precision of reference/definition results.
func (e *Engine) populateLocationPreviews(locations []core.Location) error {
	files := make(map[string][]string)
	for i := range locations {
		location := &locations[i]
		lines, ok := files[location.Path]
		if !ok {
			full, err := e.WS.Resolve(location.Path)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return core.WithCause(core.IncompleteSearch, "cannot read source line for LSP location "+location.Path, err)
			}
			lines = strings.Split(string(data), "\n")
			files[location.Path] = lines
		}
		lineIndex := location.Range.Start.Line - 1
		if lineIndex < 0 || lineIndex >= len(lines) {
			return core.NewError(core.IncompleteSearch, "LSP location is outside current source for "+location.Path)
		}
		line := strings.TrimSuffix(lines[lineIndex], "\r")
		const maxBytes = 240
		if len(line) > maxBytes {
			snippet := line[:maxBytes]
			for !utf8.ValidString(snippet) {
				snippet = snippet[:len(snippet)-1]
			}
			line = snippet + "…"
		}
		location.Preview = line
	}
	return nil
}
