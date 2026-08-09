package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConfigFile is the default configuration filename stored in workspace root.
const ConfigFile = ".simple-lsp.json"

// Server describes the command used to start one language server.
type Server struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Runtime holds the server configuration and request limits used at runtime.
type Runtime struct {
	Workspace       string
	Servers         map[string]Server
	RequestTimeout  time.Duration
	DiagnosticsWait time.Duration
	MaxResults      int
}

// allowedProfiles keeps configuration aligned with the supported language profiles.
var allowedProfiles = map[string]struct{}{
	"python": {}, "typescript-javascript": {}, "go": {}, "html": {}, "css": {},
}

// DefaultProfiles holds the built-in preset configurations.
var DefaultProfiles = map[string]Server{
	"python": {
		Command: "pyright-langserver",
		Args:    []string{"--stdio"},
	},
	"typescript-javascript": {
		Command: "typescript-language-server",
		Args:    []string{"--stdio"},
	},
	"go": {
		Command: "gopls",
		Args:    []string{},
	},
	"html": {
		Command: "npx",
		Args:    []string{"--yes", "--package=vscode-langservers-extracted", "vscode-html-language-server", "--stdio"},
	},
	"css": {
		Command: "npx",
		Args:    []string{"--yes", "--package=vscode-langservers-extracted", "vscode-css-language-server", "--stdio"},
	},
}

// Load applies defaults and reads configured language-server profiles from .simple-lsp.json.
// If .simple-lsp.json does not exist in the workspace, it is automatically created with DefaultProfiles.
func Load(base Runtime) (Runtime, error) {
	applyDefaults(&base)

	configPath := filepath.Join(base.Workspace, ConfigFile)
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := writeDefaultConfig(configPath); err != nil {
			return base, fmt.Errorf("failed to create default config %s: %w", configPath, err)
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return base, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	servers, err := loadServers(data)
	if err != nil {
		return base, fmt.Errorf("invalid config file %s: %w", configPath, err)
	}

	base.Servers = servers
	return base, nil
}

func applyDefaults(runtime *Runtime) {
	if runtime.RequestTimeout == 0 {
		runtime.RequestTimeout = 15 * time.Second
	}

	if runtime.DiagnosticsWait == 0 {
		runtime.DiagnosticsWait = 2 * time.Second
	}

	if runtime.MaxResults == 0 {
		runtime.MaxResults = 500
	}
}

func writeDefaultConfig(path string) error {
	data, err := json.MarshalIndent(DefaultProfiles, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func loadServers(data []byte) (map[string]Server, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("config file must contain a valid JSON object")
	}

	profiles, err := decodeProfiles(data)
	if err != nil {
		return nil, err
	}
	servers := make(map[string]Server, len(profiles))
	for name, raw := range profiles {
		if _, ok := allowedProfiles[name]; !ok {
			return nil, fmt.Errorf("unknown LSP profile %q", name)
		}
		server, err := decodeServer(name, raw)
		if err != nil {
			return nil, err
		}
		servers[name] = server
	}
	return servers, nil
}

func decodeProfiles(data []byte) (map[string]json.RawMessage, error) {
	var profiles map[string]json.RawMessage
	if err := json.Unmarshal(data, &profiles); err != nil || profiles == nil {
		if err != nil {
			return nil, fmt.Errorf("config must be a JSON object: %w", err)
		}
		return nil, errors.New("config must be a JSON object")
	}
	return profiles, nil
}

func decodeServer(name string, raw json.RawMessage) (Server, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Server{}, fmt.Errorf("profile %q must be an object", name)
	}

	server := Server{}
	command, ok := fields["command"]
	if !ok || json.Unmarshal(command, &server.Command) != nil || strings.TrimSpace(server.Command) == "" {
		return Server{}, fmt.Errorf("profile %q command must be a non-empty string", name)
	}
	args, ok := fields["args"]
	args = bytes.TrimSpace(args)
	if !ok || len(args) == 0 || args[0] != '[' || json.Unmarshal(args, &server.Args) != nil {
		return Server{}, fmt.Errorf("profile %q args must be an array of strings", name)
	}
	return server, nil
}

