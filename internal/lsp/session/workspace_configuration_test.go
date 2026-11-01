package session

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/transport"
)

func TestWorkspaceConfigurationReturnsAllRequestedSections(t *testing.T) {
	settings := map[string]any{
		"python": map[string]any{
			"analysis": map[string]any{"typeCheckingMode": "strict"},
		},
		"typescript.preferences": map[string]any{"includePackageJsonAutoImports": "off"},
		"enabled": false,
	}
	s := New("python", t.TempDir(), config.Server{Settings: settings})
	params := json.RawMessage(`{"items":[
		{"scopeUri":"file:///workspace/main.py","section":"python.analysis"},
		{"section":"typescript.preferences"},
		{"section":"enabled"},
		{"section":"python.missing"},
		{"section":"python.analysis.typeCheckingMode"},
		{}
	]}`)
	value, responseErr := s.serverRequest(context.Background(), transport.Message{
		Method: "workspace/configuration", Params: params,
	})
	if responseErr != nil {
		t.Fatalf("workspace/configuration error: %v", responseErr)
	}
	got, ok := value.([]any)
	if !ok {
		t.Fatalf("configuration result type %T, want []any", value)
	}
	want := []any{
		map[string]any{"typeCheckingMode": "strict"},
		map[string]any{"includePackageJsonAutoImports": "off"},
		false,
		nil,
		"strict",
		settings,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace/configuration = %#v, want %#v", got, want)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip []any
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || len(roundTrip) != len(want) {
		t.Fatalf("LSP JSON response is invalid: %s (%v)", encoded, err)
	}
}

func TestWorkspaceConfigurationHandlesEmptyAndInvalidRequests(t *testing.T) {
	s := New("typescript-javascript", t.TempDir(), config.Server{})
	for _, tc := range []struct {
		name string
		params string
		want []any
		invalid bool
	}{
		{name: "empty items", params: `{"items":[]}`, want: []any{}},
		{name: "default settings", params: `{"items":[{},{"section":"unknown"}]}`, want: []any{map[string]any{}, nil}},
		{name: "missing items", params: `{}`, invalid: true},
		{name: "malformed", params: `{"items":42}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, responseErr := s.serverRequest(context.Background(), transport.Message{
				Method: "workspace/configuration", Params: json.RawMessage(tc.params),
			})
			if tc.invalid {
				if responseErr == nil || responseErr.Code != -32602 {
					t.Fatalf("invalid request: response=%#v, error=%#v", got, responseErr)
				}
				return
			}
			if responseErr != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("response=%#v, error=%#v; want %#v", got, responseErr, tc.want)
			}
		})
	}
}
