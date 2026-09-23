package main

import "testing"

func TestSetupCommandUsesOfficialCLIAndRejectsUnknownClients(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		command, args, err := setupCommand(name, "/tmp/simple-lsp-mcp")
		if err != nil || command != name || len(args) != 5 || args[0] != "mcp" || args[1] != "add" || args[2] != "simple-lsp" || args[3] != "--" || args[4] != "/tmp/simple-lsp-mcp" {
			t.Fatalf("%s: command=%s args=%#v err=%v", name, command, args, err)
		}
	}
	if _, _, err := setupCommand("unknown", "/tmp/simple-lsp-mcp"); err == nil {
		t.Fatal("unsupported client accepted")
	}
}
