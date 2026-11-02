package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// A coding agent may use find_references as exhaustive change-impact evidence.
// A valid location followed by an invalid one must never be returned as a
// successful, complete single-location result.
func TestMultiLocationResultsNeverSilentlyDropUnresolvableEntries(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(ws.Root(), "sample.go")
	if err := os.WriteFile(full, []byte("package sample\nvar x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{MaxResults: 10})
	uri := normalize.FileURI(full)
	outsideURI := normalize.FileURI(filepath.Join(t.TempDir(), "outside.go"))
	validRange := protocol.Range{
		Start: protocol.Position{Line: 1, Character: 4},
		End:   protocol.Position{Line: 1, Character: 5},
	}
	invalidRange := protocol.Range{
		Start: protocol.Position{Line: 1, Character: 999},
		End:   protocol.Position{Line: 1, Character: 1000},
	}
	good := protocol.Location{URI: uri, Range: validRange}
	goodLink := protocol.LocationLink{
		TargetURI: uri, TargetRange: validRange, TargetSelectionRange: validRange,
	}
	cases := []struct {
		name    string
		payload any
		wantErr bool
	}{
		{"valid locations", []protocol.Location{good, good}, false},
		{"valid links", []protocol.LocationLink{goodLink, goodLink}, false},
		{"outside-workspace location", []protocol.Location{good, {URI: outsideURI, Range: validRange}}, true},
		{"invalid-range location", []protocol.Location{good, {URI: uri, Range: invalidRange}}, true},
		{"outside-workspace link", []protocol.LocationLink{goodLink, {TargetURI: outsideURI, TargetRange: validRange, TargetSelectionRange: validRange}}, true},
		{"invalid-range link", []protocol.LocationLink{goodLink, {TargetURI: uri, TargetRange: invalidRange, TargetSelectionRange: invalidRange}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.locations(raw, "utf-16")
			if tc.wantErr {
				var appErr *core.AppError
				if !errors.As(err, &appErr) || appErr.Code != core.IncompleteSearch {
					t.Fatalf("partial LSP reply was accepted: locations=%+v err=%v", got, err)
				}
				if len(got) != 0 {
					t.Fatalf("partial result leaked despite incomplete-search error: %+v", got)
				}
				return
			}
			if err != nil || len(got) != 2 {
				t.Fatalf("valid LSP reply: locations=%+v err=%v", got, err)
			}
		})
	}
}
