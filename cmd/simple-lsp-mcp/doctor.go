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
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

type doctorServer struct {
	Profile    string `json:"profile"`
	Directory  string `json:"directory"`
	Command    string `json:"command"`
	Executable string `json:"executable,omitempty"`
	Available  bool   `json:"available"`
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

// runDoctor reads an existing configuration and checks executable availability.
// It never invokes an installer or creates a default configuration. --probe
// additionally starts the LSP and makes a real documentSymbol request.
func runDoctor(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("workspace", ".", "workspace root")
	probe := flags.String("probe", "", "optional workspace-relative source file to verify with its LSP")
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
	if _, err := os.Stat(configPath); err != nil {
		report.Issues = append(report.Issues, "configuration not found; use the onboard MCP tool to create .simple-lsp.yaml")
	} else {
		runtime, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: 15 * time.Second, MaxResults: 100})
		if err != nil {
			report.Issues = append(report.Issues, "invalid configuration: "+err.Error())
		} else {
			profiles := make([]string, 0, len(runtime.Servers))
			for profile := range runtime.Servers {
				profiles = append(profiles, profile)
			}
			sort.Strings(profiles)
			for _, profile := range profiles {
				for _, server := range runtime.Servers[profile] {
					found, lookErr := exec.LookPath(server.Command)
					entry := doctorServer{Profile: profile, Directory: server.Directory, Command: server.Command, Executable: found, Available: lookErr == nil}
					report.Servers = append(report.Servers, entry)
					if lookErr != nil {
						report.Issues = append(report.Issues, fmt.Sprintf("%s: executable %q not found", profile, server.Command))
					}
				}
			}
			if *probe != "" {
				report.ProbeFile = *probe
				engine := tools.New(ws, runtime)
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
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
