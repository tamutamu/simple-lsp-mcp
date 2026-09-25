package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/transport"
)

func TestDecodeCapsDetectsHoverProvider(t *testing.T) {
	if !decodeCaps(map[string]json.RawMessage{"hoverProvider": json.RawMessage("true")}, "utf-16").Hover {
		t.Fatal("Hover = false, want true")
	}
	if decodeCaps(map[string]json.RawMessage{"hoverProvider": json.RawMessage("false")}, "utf-16").Hover {
		t.Fatal("Hover = true, want false")
	}
	if decodeCaps(map[string]json.RawMessage{}, "utf-16").Hover {
		t.Fatal("Hover = true, want false when absent")
	}
}

func TestSessionStartsMCPConfiguredCommandAndArgs(t *testing.T) {
	t.Setenv("SIMPLE_LSP_FAKE_SERVER", "1")
	tempDir := t.TempDir()
	content := fmt.Sprintf(".:\n  go:\n    command: %q\n    args:\n      - \"-test.run=^TestFakeLanguageServer$\"\n      - \"--\"\n", os.Args[0])
	if err := os.WriteFile(filepath.Join(tempDir, config.ConfigFile), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runtime, err := config.Load(config.Runtime{Workspace: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	s := New("go", tempDir, runtime.Servers["go"][0])
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPassesConfiguredEnvSettingsAndInitializationOptions(t *testing.T) {
	t.Setenv("SIMPLE_LSP_FAKE_SERVER", "1")
	t.Setenv("SIMPLE_LSP_FAKE_SERVER_ASSERT_CONFIG", "1")
	tempDir := t.TempDir()
	s := New("go", tempDir, config.Server{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestFakeLanguageServer$", "--"},
		Env:     map[string]string{"SIMPLE_LSP_CHILD_OPTION": "configured"},
		Settings: map[string]any{
			"analysis": map[string]any{"mode": "strict"},
		},
		InitializationOptions: map[string]any{
			"semanticTokens": true,
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRejectsUnconfiguredProfile(t *testing.T) {
	_, err := NewManager(t.TempDir(), map[string][]config.Server{}).For("go")
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.UnsupportedLanguage {
		t.Fatalf("For error = %v", err)
	}
}

func TestFakeLanguageServer(t *testing.T) {
	if os.Getenv("SIMPLE_LSP_FAKE_SERVER") != "1" {
		return
	}
	r := bufio.NewReader(os.Stdin)
	request, err := readMessage(r)
	if err != nil || request.Method != "initialize" || request.ID == nil {
		t.Fatalf("initialize = %#v, %v", request, err)
	}
	if os.Getenv("SIMPLE_LSP_FAKE_SERVER_ASSERT_CONFIG") == "1" {
		if os.Getenv("SIMPLE_LSP_CHILD_OPTION") != "configured" {
			t.Fatalf("configured child environment was not propagated")
		}
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Fatal(err)
		}
		init, _ := params["initializationOptions"].(map[string]any)
		if init["semanticTokens"] != true {
			t.Fatalf("initializationOptions = %#v", init)
		}
	}
	writeMessage(t, transport.Message{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{"capabilities":{}}`)})
	if notification, err := readMessage(r); err != nil || notification.Method != "initialized" {
		t.Fatalf("initialized = %#v, %v", notification, err)
	}
	if os.Getenv("SIMPLE_LSP_FAKE_SERVER_ASSERT_CONFIG") == "1" {
		configuration, err := readMessage(r)
		if err != nil || configuration.Method != "workspace/didChangeConfiguration" {
			t.Fatalf("configuration = %#v, %v", configuration, err)
		}
		var params map[string]any
		if err := json.Unmarshal(configuration.Params, &params); err != nil {
			t.Fatal(err)
		}
		settings, _ := params["settings"].(map[string]any)
		analysis, _ := settings["analysis"].(map[string]any)
		if analysis["mode"] != "strict" {
			t.Fatalf("settings = %#v", settings)
		}
	}
}

func readMessage(r *bufio.Reader) (transport.Message, error) {
	var length int
	if _, err := fmt.Fscanf(r, "Content-Length: %d\r\n\r\n", &length); err != nil {
		return transport.Message{}, err
	}
	body := make([]byte, length)
	if _, err := r.Read(body); err != nil {
		return transport.Message{}, err
	}
	var message transport.Message
	return message, json.Unmarshal(body, &message)
}

func writeMessage(t *testing.T, message transport.Message) {
	t.Helper()
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		t.Fatal(err)
	}
}

func TestForAllIncludesEveryMonorepoServer(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, map[string][]config.Server{"go": {
		{Command: "gopls", Directory: "apps/a"},
		{Command: "gopls", Directory: "apps/b"},
	}})
	sessions, err := manager.ForAll("go")
	if err != nil || len(sessions) != 2 || sessions[0] == sessions[1] {
		t.Fatalf("ForAll = %v, %v", sessions, err)
	}
	a, err := manager.ForPath("go", "apps/a/x.go")
	if err != nil || a != sessions[0] {
		t.Fatalf("ForPath(a) = %v, %v", a, err)
	}
	b, err := manager.ForPath("go", "apps/b/x.go")
	if err != nil || b != sessions[1] {
		t.Fatalf("ForPath(b) = %v, %v", b, err)
	}
}
