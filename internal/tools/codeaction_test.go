package tools

import (
	"encoding/json"
	"testing"
)

func TestCodeActionIDIsStableAcrossJSONKeyOrder(t *testing.T) {
	a := json.RawMessage(`{"title":"Fix","kind":"quickfix","data":{"b":2,"a":1}}`)
	b := json.RawMessage(`{"data":{"a":1,"b":2},"kind":"quickfix","title":"Fix"}`)
	if codeActionID(a) != codeActionID(b) {
		t.Fatalf("action IDs differ: %s != %s", codeActionID(a), codeActionID(b))
	}
}

func TestDecodeCodeActionDistinguishesBareCommand(t *testing.T) {
	bare := json.RawMessage(`{"title":"Run fix","command":"vendor.fix","arguments":[]}`)
	if _, ok := decodeCodeAction(bare); ok {
		t.Fatal("bare LSP Command was misclassified as CodeAction")
	}
	action := json.RawMessage(`{"title":"Fix","kind":"quickfix","command":{"title":"finish","command":"vendor.finish"},"edit":{"changes":{}}}`)
	got, ok := decodeCodeAction(action)
	if !ok || got.Command == nil || got.Edit == nil {
		t.Fatalf("CodeAction with nested command/edit was not decoded: %#v", got)
	}
}
