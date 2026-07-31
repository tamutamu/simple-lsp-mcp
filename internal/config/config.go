package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const MCPServersEnv = "LSP_MCP_SERVERS"

type Server struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}
type Runtime struct {
	Workspace       string
	Servers         map[string]Server
	RequestTimeout  time.Duration
	DiagnosticsWait time.Duration
	MaxResults      int
}

var allowedProfiles = map[string]struct{}{
	"python": {}, "typescript-javascript": {}, "go": {}, "html": {}, "css": {},
}

func Load(base Runtime) (Runtime, error) {
	applyDefaults(&base)
	servers, err := loadServers(os.Getenv(MCPServersEnv))
	if err != nil {
		return base, err
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

func loadServers(value string) (map[string]Server, error) {
	if value == "" {
		return map[string]Server{}, nil
	}

	profiles, err := decodeProfiles(value)
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

func decodeProfiles(value string) (map[string]json.RawMessage, error) {
	var profiles map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &profiles); err != nil || profiles == nil {
		if err != nil {
			return nil, fmt.Errorf("%s must be a JSON object: %w", MCPServersEnv, err)
		}
		return nil, fmt.Errorf("%s must be a JSON object", MCPServersEnv)
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
