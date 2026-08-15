package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/transport"
)

type Session struct {
	key, root   string
	cfg         config.Server
	mu          sync.RWMutex
	ready       chan struct{}
	starting    bool
	cmd         *exec.Cmd
	t           *transport.Transport
	caps        protocol.Capabilities
	crashed     error
	diagnostics map[string][]protocol.Diagnostic
}

func New(key, root string, cfg config.Server) *Session {
	return &Session{key: key, root: root, cfg: cfg, diagnostics: map[string][]protocol.Diagnostic{}}
}
func (s *Session) Ensure(ctx context.Context) error {
	s.mu.Lock()
	if s.t != nil && s.crashed == nil {
		s.mu.Unlock()
		return nil
	}
	if s.starting {
		ch := s.ready
		s.mu.Unlock()
		select {
		case <-ch:
			return s.status()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.starting = true
	s.ready = make(chan struct{})
	ch := s.ready
	s.mu.Unlock()
	err := s.start(ctx)
	s.mu.Lock()
	s.starting = false
	if err != nil {
		s.crashed = err
	}
	close(ch)
	s.mu.Unlock()
	return err
}
func (s *Session) start(ctx context.Context) error {
	cmd, transport, err := s.launch()
	if err != nil {
		return err
	}
	s.watch(transport)

	caps, err := s.initialize(ctx, cmd, transport)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.cmd = cmd
	s.t = transport
	s.caps = caps
	s.crashed = nil
	s.mu.Unlock()
	return nil
}

func (s *Session) launch() (*exec.Cmd, *transport.Transport, error) {
	if s.cfg.Command == "" {
		return nil, nil, core.NewError(core.LanguageServerStartFailed, "language server command is empty")
	}
	cmd := exec.Command(s.cfg.Command, s.cfg.Args...)
	cmd.Dir = s.root

	// Apply custom environment variables
	if len(s.cfg.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range s.cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); errors.Is(err, exec.ErrNotFound) {
		return nil, nil, core.WithCause(core.LanguageServerNotFound, "language server executable not found", err)
	} else if err != nil {
		return nil, nil, core.WithCause(core.LanguageServerStartFailed, "language server failed to start", err)
	}
	t := transport.New(out, in)
	t.OnRequest(s.serverRequest)
	t.OnNotification(s.notification)
	return cmd, t, nil
}

func (s *Session) watch(t *transport.Transport) {
	go func() {
		err := t.Run(context.Background())
		s.mu.Lock()
		if s.t == t {
			s.crashed = err
		}
		s.mu.Unlock()
	}()
}

func (s *Session) initialize(ctx context.Context, cmd *exec.Cmd, t *transport.Transport) (protocol.Capabilities, error) {
	var result struct {
		Capabilities     map[string]json.RawMessage `json:"capabilities"`
		PositionEncoding string                     `json:"positionEncoding"`
	}
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := t.Request(initCtx, "initialize", s.initializationParams(), &result); err != nil {
		_ = cmd.Process.Kill()
		return protocol.Capabilities{}, core.WithCause(core.LanguageServerStartFailed, "language server initialize failed", err)
	}
	if result.PositionEncoding == "" {
		result.PositionEncoding = "utf-16"
	}
	if err := t.Notify("initialized", map[string]any{}); err != nil {
		return protocol.Capabilities{}, err
	}
	settings := s.cfg.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	if err := t.Notify("workspace/didChangeConfiguration", map[string]any{"settings": settings}); err != nil {
		return protocol.Capabilities{}, err
	}
	return decodeCaps(result.Capabilities, result.PositionEncoding), nil
}

func (s *Session) initializationParams() map[string]any {
	sessionRoot := s.root

	if s.key == "go" {
		initOpts := map[string]any{"symbolMatcher": "CaseInsensitive"}
		for k, v := range s.cfg.InitializationOptions {
			initOpts[k] = v
		}
		return map[string]any{
			"rootUri": fileURI(sessionRoot),
			"capabilities": map[string]any{
				"workspace":    map[string]any{"configuration": true},
				"textDocument": map[string]any{"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true}},
			},
			"initializationOptions": initOpts,
		}
	}
	params := map[string]any{
		"processId":        nil,
		"clientInfo":       map[string]string{"name": "simple-lsp-mcp"},
		"rootUri":          fileURI(sessionRoot),
		"workspaceFolders": []map[string]string{{"uri": fileURI(sessionRoot), "name": "workspace"}},
		"capabilities":     clientCapabilities(),
		"general":          map[string]any{"positionEncodings": []string{"utf-8", "utf-16", "utf-32"}},
	}
	if len(s.cfg.InitializationOptions) > 0 {
		params["initializationOptions"] = s.cfg.InitializationOptions
	}
	return params
}
func (s *Session) status() error { s.mu.RLock(); defer s.mu.RUnlock(); return s.crashed }
func (s *Session) Request(ctx context.Context, method string, params, result any) error {
	if err := s.Ensure(ctx); err != nil {
		return err
	}
	s.mu.RLock()
	t := s.t
	s.mu.RUnlock()
	err := t.Request(ctx, method, params, result)
	if errors.Is(err, context.DeadlineExceeded) {
		return core.NewError(core.RequestTimeout, "language server request timed out")
	}
	return err
}
func (s *Session) Notify(method string, params any) error {
	s.mu.RLock()
	t := s.t
	s.mu.RUnlock()
	if t == nil {
		return core.NewError(core.LanguageServerStartFailed, "language server is not ready")
	}
	return t.Notify(method, params)
}
func (s *Session) Capabilities() protocol.Capabilities {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.caps
}
func (s *Session) Diagnostics(uri string) []protocol.Diagnostic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]protocol.Diagnostic(nil), s.diagnostics[uri]...)
}
func (s *Session) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	t, cmd := s.t, s.cmd
	s.mu.RUnlock()
	if t == nil {
		return nil
	}
	_ = t.Request(ctx, "shutdown", map[string]any{}, nil)
	_ = t.Notify("exit", map[string]any{})
	if cmd != nil && cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		}
	}
	return nil
}
func (s *Session) serverRequest(_ context.Context, m transport.Message) (any, *transport.ResponseError) {
	switch m.Method {
	case "workspace/configuration":
		return []map[string]any{{}}, nil
	case "workspace/workspaceFolders":
		return []map[string]string{{"uri": fileURI(s.root), "name": "workspace"}}, nil
	case "workspace/applyEdit":
		return map[string]any{"applied": false, "failureReason": "read-only server"}, nil
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create":
		return map[string]any{}, nil
	default:
		return nil, &transport.ResponseError{Code: -32601, Message: "method not supported"}
	}
}
func (s *Session) notification(m transport.Message) {
	if m.Method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string                `json:"uri"`
		Diagnostics []protocol.Diagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(m.Params, &p) == nil {
		s.mu.Lock()
		s.diagnostics[p.URI] = p.Diagnostics
		s.mu.Unlock()
	}
}
func fileURI(path string) string { return "file://" + path }
func clientCapabilities() map[string]any {
	return map[string]any{"workspace": map[string]any{"workspaceFolders": true, "didChangeConfiguration": map[string]any{"dynamicRegistration": false}, "symbol": map[string]any{"dynamicRegistration": false}, "diagnostics": map[string]any{}}, "textDocument": map[string]any{"synchronization": map[string]any{"didSave": true}, "documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true}, "definition": map[string]any{"linkSupport": true}, "references": map[string]any{}, "implementation": map[string]any{"linkSupport": true}, "typeDefinition": map[string]any{"linkSupport": true}, "declaration": map[string]any{"linkSupport": true}, "callHierarchy": map[string]any{}, "typeHierarchy": map[string]any{}, "diagnostic": map[string]any{}}}
}
func decodeCaps(m map[string]json.RawMessage, encoding string) protocol.Capabilities {
	has := func(k string) bool { b := m[k]; return len(b) > 0 && string(b) != "false" && string(b) != "null" }
	return protocol.Capabilities{PositionEncoding: encoding, WorkspaceSymbol: has("workspaceSymbolProvider"), DocumentSymbol: has("documentSymbolProvider"), Definition: has("definitionProvider"), References: has("referencesProvider"), Implementation: has("implementationProvider"), TypeDefinition: has("typeDefinitionProvider"), Declaration: has("declarationProvider"), CallHierarchy: has("callHierarchyProvider"), TypeHierarchy: has("typeHierarchyProvider"), Diagnostics: has("diagnosticProvider"), WorkspaceDiagnostics: has("diagnosticProvider")}
}

type Manager struct {
	root     string
	servers  map[string][]config.Server
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager(root string, servers map[string][]config.Server) *Manager {
	return &Manager{root: root, servers: servers, sessions: map[string]*Session{}}
}

func (m *Manager) For(key string) (*Session, error) {
	return m.ForPath(key, "")
}

func (m *Manager) ForPath(key string, targetPath string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	serverList, ok := m.servers[key]
	if !ok || len(serverList) == 0 {
		return nil, core.NewError(core.UnsupportedLanguage, "no language server profile")
	}

	srv := config.SelectServer(serverList, targetPath)

	cacheKey := key
	if srv.Directory != "" && srv.Directory != "." {
		cacheKey = key + ":" + srv.Directory
	}

	if s := m.sessions[cacheKey]; s != nil {
		return s, nil
	}

	sessionRoot := m.root
	if srv.Directory != "" && srv.Directory != "." {
		if filepath.IsAbs(srv.Directory) {
			sessionRoot = srv.Directory
		} else {
			sessionRoot = filepath.Join(m.root, srv.Directory)
		}
	}

	s := New(key, sessionRoot, srv)
	m.sessions[cacheKey] = s
	return s, nil
}

func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	ss := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		ss = append(ss, s)
	}
	m.mu.Unlock()
	for _, s := range ss {
		_ = s.Shutdown(ctx)
	}
}

var _ io.Closer
