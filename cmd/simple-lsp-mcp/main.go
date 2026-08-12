package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/mcpserver"
	"github.com/tamutamu/simple-lsp-mcp/internal/onboard"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runInitCmd(os.Args[2:])
		return
	}

	var root, logLevel string
	var timeout, wait time.Duration
	var max int

	// Parse server settings before starting the stdio transport.
	flag.StringVar(&root, "workspace", "", "workspace root (default: current working directory)")
	flag.StringVar(&logLevel, "log-level", "info", "debug|info|warn|error")
	flag.DurationVar(&timeout, "request-timeout", 15*time.Second, "LSP request timeout")
	flag.DurationVar(&wait, "diagnostics-wait", 2*time.Second, "push diagnostics wait")
	flag.IntVar(&max, "max-results", 500, "maximum result count")
	flag.Parse()
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

	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: timeout, DiagnosticsWait: wait, MaxResults: max})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	engine := tools.New(ws, cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcpserver.New(engine, version)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		slog.Error("mcp server stopped", "error", err)
	}

	// Let active language servers exit cleanly before forcing termination.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	engine.Sessions.Shutdown(shutdownCtx)
}

func runInitCmd(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	var ws string
	var yes bool
	fs.StringVar(&ws, "workspace", "", "workspace root (default: current working directory)")
	fs.BoolVar(&yes, "yes", false, "overwrite existing configuration without prompt")
	_ = fs.Parse(args)

	res, err := onboard.Run(onboard.Options{
		Workspace: ws,
		Overwrite: yes,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Initialization failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully generated configuration at %s\n\nDetected projects:\n", res.ConfigPath)
	for path, profs := range res.Detected {
		fmt.Printf("  [%s]: %v\n", path, profs)
	}
}
