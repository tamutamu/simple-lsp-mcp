package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/transport"
)

func TestSessionStartsMCPConfiguredCommandAndArgs(t *testing.T) {
	t.Setenv("SIMPLE_LSP_FAKE_SERVER", "1")
	value, err := json.Marshal(map[string]any{"go": map[string]any{
		"command": os.Args[0],
		"args":    []string{"-test.run=^TestFakeLanguageServer$", "--"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.MCPServersEnv, string(value))
	runtime, err := config.Load(config.Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	s := New("go", t.TempDir(), runtime.Servers["go"])
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRejectsUnconfiguredProfile(t *testing.T) {
	_, err := NewManager(t.TempDir(), map[string]config.Server{}).For("go")
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
	writeMessage(t, transport.Message{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{"capabilities":{}}`)})
	if notification, err := readMessage(r); err != nil || notification.Method != "initialized" {
		t.Fatalf("initialized = %#v, %v", notification, err)
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
