package config

import "sort"

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

// automaticServer selects an isolated, pinned LSP installation in managed mode.
// Custom YAML always takes precedence, and un-managed mode preserves the
// installed-server behavior without downloading anything.
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
	if allowDownload {
		// The session manager resolves this to an absolute cached executable.
		return Server{Managed: true, Command: "managed:" + profile}
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
