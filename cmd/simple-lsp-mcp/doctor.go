package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/managedlsp"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

type doctorServer struct {
	Profile    string   `json:"profile"`
	Directory  string   `json:"directory"`
	Command    string   `json:"command"`
	Executable string   `json:"executable,omitempty"`
	Available  bool     `json:"available"`
	Managed    bool     `json:"managed,omitempty"`
	Args       []string `json:"args,omitempty"`
	Cached     bool     `json:"cached,omitempty"`
}

type doctorReport struct {
	Version    string         `json:"version"`
	Binary     string         `json:"binary"`
	PATHBinary string         `json:"path_binary,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
	Workspace  string         `json:"workspace"`
	Config     string         `json:"config"`
	Servers    []doctorServer `json:"servers"`
	ProbeFile  string         `json:"probe_file,omitempty"`
	ProbeOK    bool           `json:"probe_ok,omitempty"`
	Issues     []string       `json:"issues,omitempty"`
	OK         bool           `json:"ok"`
}

// runDoctor checks explicit or automatically detected profiles. The default
// mode does not install packages. --probe starts the matching LSP and can
// download packages only when --auto-install-lsp was explicitly provided.
func runDoctor(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("workspace", ".", "workspace root")
	probe := flags.String("probe", "", "optional workspace-relative source file to verify with its LSP")
	autoInstall := flags.Bool("auto-install-lsp", false, "preview managed first-use package installation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("doctor: unexpected arguments: %v", flags.Args())
	}
	ws, err := workspace.Open(*root)
	if err != nil {
		return err
	}
	report := doctorReport{Version: getVersion(), Workspace: ws.Root(), Servers: []doctorServer{}}
	if executable, executableErr := os.Executable(); executableErr == nil {
		report.Binary = executable
		current := executable
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			current = resolved
		}
		if pathBinary, lookErr := exec.LookPath("simple-lsp-mcp"); lookErr == nil {
			report.PATHBinary = pathBinary
			pathResolved := pathBinary
			if resolved, resolveErr := filepath.EvalSymlinks(pathBinary); resolveErr == nil {
				pathResolved = resolved
			}
			if pathResolved != current {
				report.Warnings = append(report.Warnings, fmt.Sprintf("PATH resolves simple-lsp-mcp to %s, not the running binary %s; clients configured by command name may launch a different version", pathBinary, executable))
			}
		}
	}
	configPath := filepath.Join(ws.Root(), config.ConfigFile)
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		configPath = filepath.Join(ws.Root(), config.ConfigFileAlt)
	}
	report.Config = configPath
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		report.Config = "auto-detected (in memory)"
	}
	runtime, err := config.Load(config.Runtime{Workspace: ws.Root(), MaxResults: 100, AutoInstallLSP: *autoInstall})
	if err != nil {
		report.Issues = append(report.Issues, "invalid configuration: "+err.Error())
	} else {
		if len(runtime.Servers) == 0 {
			report.Issues = append(report.Issues, "no supported language project detected; add a supported project marker or write .simple-lsp.yaml explicitly")
		}
		profiles := make([]string, 0, len(runtime.Servers))
		for profile := range runtime.Servers {
			profiles = append(profiles, profile)
		}
		sort.Strings(profiles)
		for _, profile := range profiles {
			for _, server := range runtime.Servers[profile] {
				entry := doctorServer{Profile: profile, Directory: server.Directory, Command: server.Command, Args: server.Args, Managed: server.Managed}
				if server.Managed {
					path, ready, missing, statusErr := managedlsp.Status(profile)
					entry.Executable, entry.Cached, entry.Available = path, ready, missing == "" && statusErr == nil
					if statusErr != nil {
						report.Issues = append(report.Issues, statusErr.Error())
					} else if missing != "" {
						report.Issues = append(report.Issues, fmt.Sprintf("%s: missing %s (required to install or run the managed LSP)", profile, missing))
					} else if !ready {
						report.Warnings = append(report.Warnings, fmt.Sprintf("%s: pinned language server will be installed in %s on first use", profile, path))
					}
				} else {
					found, lookErr := exec.LookPath(server.Command)
					entry.Executable, entry.Available = found, lookErr == nil
					if lookErr != nil {
						report.Issues = append(report.Issues, fmt.Sprintf("%s: executable %q not found; install it or use --auto-install-lsp", profile, server.Command))
					}
				}
				report.Servers = append(report.Servers, entry)
			}
		}
		if *probe != "" {
			report.ProbeFile = *probe
			engine := tools.New(ws, runtime)
			probeTimeout := 25 * time.Second
			if *autoInstall {
				probeTimeout = 190 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			_, probeErr := engine.DocumentSymbols(ctx, map[string]any{"path": *probe})
			cancel()
			shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			engine.Sessions.Shutdown(shutdownCtx)
			stop()
			if probeErr != nil {
				report.Issues = append(report.Issues, "LSP document-symbol probe failed: "+probeErr.Error())
			} else {
				report.ProbeOK = true
			}
		}
	}
	report.OK = len(report.Issues) == 0
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	if !report.OK {
		return fmt.Errorf("doctor found %d issue(s)", len(report.Issues))
	}
	return nil
}