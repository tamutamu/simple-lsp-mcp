package main

import (
	"reflect"
	"testing"
)

func TestSetupCommandUsesOfficialCLIAndRejectsUnknownClients(t *testing.T) {
	for _, tc := range []struct {
		client   string
		expected []string
	}{
		{"claude", []string{"mcp", "add", "--scope", "user", "simple-lsp", "--", "/tmp/simple-lsp-mcp"}},
		{"codex", []string{"mcp", "add", "simple-lsp", "--", "/tmp/simple-lsp-mcp"}},
	} {
		command, args, err := setupCommand(tc.client, "/tmp/simple-lsp-mcp")
		if err != nil || command != tc.client || !reflect.DeepEqual(args, tc.expected) {
			t.Fatalf("%s: command=%s args=%#v err=%v", tc.client, command, args, err)
		}
	}
	if _, _, err := setupCommand("unknown", "/tmp/simple-lsp-mcp"); err == nil {
		t.Fatal("unsupported client accepted")
	}
}

func TestSetupCommandManagedModeUsesOneRegistration(t *testing.T) {
	for _, client := range []string{"claude", "codex"} {
		name, args, err := setupCommandOptions(client, "/tmp/simple-lsp-mcp", true)
		if err != nil || name != client || len(args) != map[string]int{"claude": 8, "codex": 6}[client] || args[len(args)-1] != "--auto-install-lsp" {
			t.Fatalf("client=%s command=%s args=%#v err=%v", client, name, args, err)
		}
	}
}
