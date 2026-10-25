package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/mcpserver"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

var version = "dev"

func getVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return version
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := runSetup(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		if err := runDoctor(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	var root, logLevel, toolProfile string
	var timeout, wait time.Duration
	var max int
	var showVersion, autoInstallLSP bool

	// Parse server settings before starting the stdio transport.
	flag.StringVar(&root, "workspace", "", "workspace root (default: current working directory)")
	flag.StringVar(&logLevel, "log-level", "info", "debug|info|warn|error")
	flag.StringVar(&toolProfile, "tool-profile", "full", "MCP tool surface: full (all 19), coding (11 daily navigation/inspection tools)")
	flag.DurationVar(&timeout, "request-timeout", 0, "LSP request timeout (default 15s, 180s with managed LSP)")
	flag.DurationVar(&wait, "diagnostics-wait", 2*time.Second, "push diagnostics wait")
	flag.IntVar(&max, "max-results", 500, "maximum result count")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&autoInstallLSP, "auto-install-lsp", false, "allow downloading pinned language servers on first use when not installed (requires Go or npx)")
	flag.Parse()

	ver := getVersion()
	if showVersion {
		fmt.Printf("simple-lsp-mcp %s\n", ver)
		return
	}
	if toolProfile != "full" && toolProfile != "coding" {
		fmt.Fprintln(os.Stderr, "invalid --tool-profile: expected full or coding")
		os.Exit(2)
	}
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to get current working directory:", err)
			os.Exit(2)
		}
	}

	level := new(slog.LevelVar)
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		fmt.Fprintln(os.Stderr, "invalid --log-level")
		os.Exit(2)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	ws, err := workspace.Open(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: timeout, DiagnosticsWait: wait, MaxResults: max, AutoInstallLSP: autoInstallLSP})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	engine := tools.New(ws, cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcpserver.NewWithProfile(engine, ver, toolProfile)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		slog.Error("mcp server stopped", "error", err)
	}

	// Let active language servers exit cleanly before forcing termination.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	engine.Sessions.Shutdown(shutdownCtx)
}
