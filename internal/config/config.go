package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFile is the primary default configuration filename stored in workspace root.
const ConfigFile = ".simple-lsp.yaml"

// ConfigFileAlt is the alternative configuration filename stored in workspace root.
const ConfigFileAlt = ".simple-lsp.yml"

// Server describes the command and runtime settings used to start one language server.
type Server struct {
	Command               string            `yaml:"command" json:"command"`
	Args                  []string          `yaml:"args" json:"args"`
	RootDir               string            `yaml:"root_dir,omitempty" json:"root_dir,omitempty"`
	Cwd                   string            `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	Pattern               string            `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Env                   map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Settings              map[string]any    `yaml:"settings,omitempty" json:"settings,omitempty"`
	InitializationOptions map[string]any    `yaml:"initialization_options,omitempty" json:"initialization_options,omitempty"`
}

// ServerList represents one or more servers configured for a language profile.
type ServerList []Server

// UnmarshalYAML implements custom unmarshaling for ServerList to accept either a single Server object or a slice of Server objects.
func (sl *ServerList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.SequenceNode {
		var list []Server
		if err := value.Decode(&list); err != nil {
			return err
		}
		*sl = list
		return nil
	}
	var single Server
	if err := value.Decode(&single); err != nil {
		return err
	}
	*sl = ServerList{single}
	return nil
}

// Runtime holds the server configuration and request limits used at runtime.
type Runtime struct {
	Workspace       string
	Servers         map[string][]Server
	RequestTimeout  time.Duration
	DiagnosticsWait time.Duration
	MaxResults      int
}

// allowedProfiles keeps configuration aligned with the supported language profiles.
var allowedProfiles = map[string]struct{}{
	"python": {}, "typescript-javascript": {}, "go": {}, "html": {}, "css": {},
}

// DefaultProfiles holds the built-in preset configurations.
var DefaultProfiles = map[string][]Server{
	"python": {
		{
			Command: "pyright-langserver",
			Args:    []string{"--stdio"},
		},
	},
	"typescript-javascript": {
		{
			Command: "typescript-language-server",
			Args:    []string{"--stdio"},
		},
	},
	"go": {
		{
			Command: "gopls",
			Args:    []string{},
		},
	},
	"html": {
		{
			Command: "npx",
			Args:    []string{"--yes", "--package=vscode-langservers-extracted", "vscode-html-language-server", "--stdio"},
		},
	},
	"css": {
		{
			Command: "npx",
			Args:    []string{"--yes", "--package=vscode-langservers-extracted", "vscode-css-language-server", "--stdio"},
		},
	},
}

// Load applies defaults and reads configured language-server profiles from .simple-lsp.yaml (or .simple-lsp.yml).
// If neither config file exists in the workspace, .simple-lsp.yaml is automatically created with DefaultProfiles.
func Load(base Runtime) (Runtime, error) {
	applyDefaults(&base)

	configPath := filepath.Join(base.Workspace, ConfigFile)
	altPath := filepath.Join(base.Workspace, ConfigFileAlt)

	targetPath := configPath
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if _, errAlt := os.Stat(altPath); errAlt == nil {
			targetPath = altPath
		} else {
			if err := WriteDefaultConfig(configPath); err != nil {
				return base, fmt.Errorf("failed to create default config %s: %w", configPath, err)
			}
		}
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return base, fmt.Errorf("failed to read config file %s: %w", targetPath, err)
	}

	servers, err := loadServers(data)
	if err != nil {
		return base, fmt.Errorf("invalid config file %s: %w", targetPath, err)
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

// WriteDefaultConfig writes the default profile configuration in YAML format.
func WriteDefaultConfig(path string) error {
	defaultYaml := map[string]map[string]Server{
		".": {
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
		},
	}
	data, err := yaml.Marshal(defaultYaml)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func loadServers(data []byte) (map[string][]Server, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("config file must contain valid YAML mapping")
	}

	var rawNode yaml.Node
	if err := yaml.Unmarshal(data, &rawNode); err != nil {
		return nil, fmt.Errorf("invalid YAML format: %w", err)
	}

	if rawNode.Kind == 0 || len(rawNode.Content) == 0 {
		return nil, errors.New("config file must contain valid YAML mapping")
	}

	doc := rawNode.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("config file must contain a YAML mapping")
	}

	servers := make(map[string][]Server)

	// Determine whether top-level keys are language profile names or directory paths
	for i := 0; i < len(doc.Content); i += 2 {
		keyNode := doc.Content[i]
		valNode := doc.Content[i+1]

		key := keyNode.Value

		if _, isProfile := allowedProfiles[key]; isProfile {
			// Direct profile format (treated as root "." path)
			var list ServerList
			if err := valNode.Decode(&list); err != nil {
				return nil, fmt.Errorf("invalid server configuration for profile %q: %w", key, err)
			}
			for idx, s := range list {
				if strings.TrimSpace(s.Command) == "" {
					return nil, fmt.Errorf("profile %q server [%d] command must be a non-empty string", key, idx)
				}
				if s.RootDir == "" {
					s.RootDir = "."
				}
				servers[key] = append(servers[key], s)
			}
		} else {
			// Path-keyed format (e.g. ".", "apps/frontend", "apps/backend")
			relPath := filepath.Clean(key)
			var profileMap map[string]ServerList
			if err := valNode.Decode(&profileMap); err != nil {
				return nil, fmt.Errorf("invalid profile mapping under path %q: %w", key, err)
			}
			for profName, list := range profileMap {
				if _, ok := allowedProfiles[profName]; !ok {
					return nil, fmt.Errorf("unknown LSP profile %q under path %q", profName, key)
				}
				for idx, s := range list {
					if strings.TrimSpace(s.Command) == "" {
						return nil, fmt.Errorf("path %q profile %q server [%d] command must be a non-empty string", key, profName, idx)
					}
					if s.RootDir == "" {
						s.RootDir = relPath
					}
					servers[profName] = append(servers[profName], s)
				}
			}
		}
	}

	if len(servers) == 0 {
		return nil, errors.New("config file must define at least one valid profile")
	}

	return servers, nil
}

// SelectServer matches a target file path against configured servers for a given language profile.
// It prioritizes the longest matching RootDir, defaulting to the first configured server or a zero Server if none match.
func SelectServer(servers []Server, targetPath string) Server {
	if len(servers) == 0 {
		return Server{}
	}

	cleanTarget := filepath.Clean(targetPath)
	var bestMatch Server
	bestLen := -1

	for _, s := range servers {
		rootDir := filepath.Clean(s.RootDir)
		if rootDir == "" {
			rootDir = "."
		}

		matched := false
		if rootDir == "." {
			matched = true
		} else if cleanTarget == rootDir || strings.HasPrefix(cleanTarget, rootDir+string(filepath.Separator)) {
			matched = true
		}

		if s.Pattern != "" {
			if ok, _ := filepath.Match(s.Pattern, cleanTarget); !ok {
				matched = false
			}
		}

		if matched {
			matchLen := len(rootDir)
			if rootDir == "." {
				matchLen = 0
			}
			if matchLen > bestLen {
				bestLen = matchLen
				bestMatch = s
			}
		}
	}

	if bestLen >= 0 {
		return bestMatch
	}
	return servers[0]
}
