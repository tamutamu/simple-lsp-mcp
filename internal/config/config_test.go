package config

import (
	"strings"
	"testing"
)

func TestLoadMCPServers(t *testing.T) {
	t.Setenv(MCPServersEnv, `{"python":{"command":"npx","args":["-y","pyright-langserver","--stdio"]},"typescript-javascript":{"command":"npx","args":["-y","typescript-language-server","--stdio"]},"go":{"command":"gopls","args":[]}}`)
	runtime, err := Load(Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.Servers["go"]; got.Command != "gopls" || len(got.Args) != 0 {
		t.Fatalf("go server = %#v", got)
	}
	if got := runtime.Servers["python"]; got.Command != "npx" || strings.Join(got.Args, " ") != "-y pyright-langserver --stdio" {
		t.Fatalf("python server = %#v", got)
	}
}

func TestLoadMCPHostExamples(t *testing.T) {
	for _, example := range []string{
		`{"go":{"command":"gopls","args":[]}}`,
		`{"go":{"command":"gopls","args":[]}}`,
	} {
		t.Run(example, func(t *testing.T) {
			t.Setenv(MCPServersEnv, example)
			if _, err := Load(Runtime{}); err != nil {
				t.Fatalf("MCP host configuration did not start: %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidMCPServers(t *testing.T) {
	for name, value := range map[string]string{
		"invalid JSON":    `{`,
		"unknown profile": `{"rust":{"command":"rust-analyzer","args":[]}}`,
		"empty command":   `{"go":{"command":"","args":[]}}`,
		"missing args":    `{"go":{"command":"gopls"}}`,
		"non-string args": `{"go":{"command":"gopls","args":[1]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(MCPServersEnv, value)
			if _, err := Load(Runtime{}); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestLoadWithoutMCPServersDisablesAllProfiles(t *testing.T) {
	t.Setenv(MCPServersEnv, "")
	runtime, err := Load(Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Servers) != 0 {
		t.Fatalf("servers = %#v", runtime.Servers)
	}
}
