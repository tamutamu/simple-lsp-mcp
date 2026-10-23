package config

import (
	"os/exec"
	"sort"
)

// automaticServers configures detected language roots in memory only. Installed
// binaries are reused. Downloads are only possible when AutoInstallLSP was
// explicitly enabled on startup; no installer runs during configuration load.
func automaticServers(root string, allowDownload bool) map[string][]Server {
	found := DetectWorkspace(root)
	servers := make(map[string][]Server)
	directories := make([]string, 0, len(found))
	for directory := range found {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	for _, directory := range directories {
		profiles := found[directory]
		for _, profile := range profiles {
			server := automaticServer(profile, allowDownload)
			if server.Command == "" {
				continue
			}
			server.Directory = directory
			servers[profile] = append(servers[profile], server)
		}
	}
	return servers
}

// automaticServer uses an installed executable by preference, and provides a
// pinned, on-demand managed launcher for supported languages when permitted.
// Existing .simple-lsp.yaml configuration always takes precedence.
func automaticServer(profile string, allowDownload bool) Server {
	var executable string
	var args []string
	switch profile {
	case "go":
		executable = "gopls"
		args = []string{"serve"}
	case "python":
		executable = "pyright-langserver"
		args = []string{"--stdio"}
	case "typescript-javascript":
		executable = "typescript-language-server"
		args = []string{"--stdio"}
	case "html":
		executable = "vscode-html-language-server"
		args = []string{"--stdio"}
	case "css":
		executable = "vscode-css-language-server"
		args = []string{"--stdio"}
	default:
		return Server{}
	}
	if _, err := exec.LookPath(executable); err == nil {
		return Server{Command: executable, Args: args}
	}
	if !allowDownload {
		// Make the missing dependency explicit rather than silently hiding the
		// language. Doctor will identify the missing executable.
		return Server{Command: executable, Args: args}
	}
	if profile == "go" {
		if _, err := exec.LookPath("go"); err == nil {
			return Server{Command: "go", Args: []string{"run", "golang.org/x/tools/gopls@v0.24.0", "serve"}, Managed: true}
		}
	} else if _, err := exec.LookPath("npx"); err == nil {
		switch profile {
		case "python":
			return Server{Command: "npx", Args: []string{"--yes", "--package=pyright@1.1.413", "pyright-langserver", "--stdio"}, Managed: true}
		case "typescript-javascript":
			return Server{Command: "npx", Args: []string{"--yes", "--package=typescript@5.9.3", "--package=typescript-language-server@5.3.0", "typescript-language-server", "--stdio"}, Managed: true}
		case "html":
			return Server{Command: "npx", Args: []string{"--yes", "--package=vscode-langservers-extracted@4.10.0", "vscode-html-language-server", "--stdio"}, Managed: true}
		case "css":
			return Server{Command: "npx", Args: []string{"--yes", "--package=vscode-langservers-extracted@4.10.0", "vscode-css-language-server", "--stdio"}, Managed: true}
		}
	}
	return Server{Command: executable, Args: args}
}

func hasManagedServer(servers map[string][]Server) bool {
	for _, entries := range servers {
		for _, server := range entries {
			if server.Managed {
				return true
			}
		}
	}
	return false
}
