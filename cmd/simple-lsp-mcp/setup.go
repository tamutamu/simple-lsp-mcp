package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
)

// setupCommand uses each client's own MCP CLI, not direct edits to JSON/TOML.
// The generated command is displayed first; --apply is an explicit opt-in.
func setupCommand(client, binary string) (string, []string, error) {
	switch client {
	case "claude", "codex":
		return client, []string{"mcp", "add", "simple-lsp", "--", binary}, nil
	default:
		return "", nil, fmt.Errorf("unsupported client %q: use claude or codex", client)
	}
}

func runSetup(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: simple-lsp-mcp setup claude|codex [--apply]")
	}
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apply := flags.Bool("apply", false, "explicitly register the MCP server with the client")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected setup arguments: %v", flags.Args())
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binary, command, err := func() (string, []string, error) {
		bin, err := exec.LookPath(executable)
		if err != nil {
			return "", nil, err
		}
		name, argv, err := setupCommand(args[0], bin)
		return name, argv, err
	}()
	if err != nil {
		return err
	}
	fmt.Fprint(out, "Command:", binary)
	for _, arg := range command {
		fmt.Fprint(out, " ", strconv.Quote(arg))
	}
	fmt.Fprintln(out)
	if !*apply {
		fmt.Fprintln(out, "Preview only; use --apply to register the server.")
		return nil
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("%s is not on PATH: %w", binary, err)
	}
	// Never silently overwrite a previous registration.
	if err := exec.Command(binary, "mcp", "get", "simple-lsp").Run(); err == nil {
		return fmt.Errorf("simple-lsp is already registered with %s; refusing to overwrite", binary)
	}
	action := exec.Command(binary, command...)
	action.Stdin, action.Stdout, action.Stderr = os.Stdin, out, os.Stderr
	if err := action.Run(); err != nil {
		return fmt.Errorf("%s setup failed: %w", binary, err)
	}
	return nil
}
