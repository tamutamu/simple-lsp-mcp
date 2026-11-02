package tools

import (
	"encoding/json"
	"fmt"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// decodeDocumentSymbolResponse distinguishes the two LSP documentSymbol shapes
// by their JSON fields, not by whether Unmarshal happens to succeed. A flat
// SymbolInformation also has name/kind, so decoding it as DocumentSymbol
// silently manufactures zero-valued ranges and incorrect symbol positions.
func decodeDocumentSymbolResponse(raw json.RawMessage) ([]protocol.DocumentSymbol, []protocol.SymbolInformation, bool, error) {
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, nil, false, core.WithCause(core.IncompleteSearch, "invalid documentSymbol response", err)
	}
	if len(entries) == 0 {
		// Both [] and null are valid empty documentSymbol responses.
		return []protocol.DocumentSymbol{}, nil, true, nil
	}
	var flatResponse bool
	for i, entry := range entries {
		_, hasLocation := entry["location"]
		_, hasRange := entry["range"]
		_, hasSelectionRange := entry["selectionRange"]
		if entry == nil || (hasLocation && (hasRange || hasSelectionRange)) ||
			(!hasLocation && (!hasRange || !hasSelectionRange)) {
			return nil, nil, false, core.NewError(core.IncompleteSearch,
				fmt.Sprintf("documentSymbol entry %d has an invalid response shape", i))
		}
		if i == 0 {
			flatResponse = hasLocation
		} else if flatResponse != hasLocation {
			return nil, nil, false, core.NewError(core.IncompleteSearch,
				"documentSymbol response mixes flat and hierarchical entries")
		}
	}
	if flatResponse {
		var flat []protocol.SymbolInformation
		if err := json.Unmarshal(raw, &flat); err != nil {
			return nil, nil, false, core.WithCause(core.IncompleteSearch, "invalid flat documentSymbol response", err)
		}
		return nil, flat, false, nil
	}
	var tree []protocol.DocumentSymbol
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, nil, false, core.WithCause(core.IncompleteSearch, "invalid hierarchical documentSymbol response", err)
	}
	return tree, nil, true, nil
}
