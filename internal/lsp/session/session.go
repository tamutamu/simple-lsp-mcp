package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/transport"
	"github.com/tamutamu/simple-lsp-mcp/internal/managedlsp"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

type Session struct {
	key, root   string
	cfg         config.Server
	mu          sync.RWMutex
	ready       chan struct{}
	starting    bool
	generation  uint64 // increases on each successful language-server initialization
	cmd         *exec.Cmd
	t           *transport.Transport
	caps        protocol.Capabilities
	crashed     error
	diagnostics map[string][]protocol.Diagnostic
	// The last didOpen/didChange version sent to this server. A late
	// publishDiagnostics from an older version must not be treated as a
	// clean/current report after the agent edits the file.
	documentVersions map[string]int32
	diagnosticEvent chan struct{} // broadcast when push diagnostics arrive or become stale
	pullDiagnosticsUnsupported bool // -32601 is stable until session restart
}

func New(key, root string, cfg config.Server) *Session {
	return &Session{key: key, root: root, cfg: cfg, diagnostics: map[string][]protocol.Diagnostic{}, documentVersions: map[string]int32{}, diagnosticEvent: make(chan struct{})}
}

// Generation identifies the currently initialized server incarnation.
// The document store uses it to reopen previously synced files after crash
// recovery; a newly started language server has no open documents.
func (s *Session) Generation() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation
}

// Root returns the filesystem root served by this language-server instance.
// It is used by bounded fallback discovery when workspace/symbol is unavailable
// before a server has indexed or opened a project.
func (s *Session) Root() string { return s.root }
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
	// A restarted language server has not published any diagnostics yet.
	// Never return cached diagnostics from a crashed server incarnation.
	s.mu.Lock()
	s.diagnostics = make(map[string][]protocol.Diagnostic)
	s.documentVersions = make(map[string]int32)
	s.pullDiagnosticsUnsupported = false
	s.signalDiagnosticsLocked()
	s.mu.Unlock()
	cmd, transport, err := s.launch(ctx)
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
	s.generation++
	s.crashed = nil
	s.mu.Unlock()
	return nil
}

func (s *Session) launch(ctx context.Context) (*exec.Cmd, *transport.Transport, error) {
	if s.cfg.Command == "" {
		return nil, nil, core.NewError(core.LanguageServerStartFailed, "language server command is empty")
	}
	command, args := s.cfg.Command, s.cfg.Args
	if s.cfg.Managed {
		var err error
		command, args, err = managedlsp.Ensure(ctx, s.key)
		if err != nil {
			return nil, nil, core.WithCause(core.LanguageServerStartFailed, "managed language server provisioning failed", err)
		}
	}
	cmd := exec.Command(command, args...)
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
	initializeTimeout := 15 * time.Second
	if s.cfg.Managed {
		initializeTimeout = 180 * time.Second
	}
	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
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
				"workspace": map[string]any{"configuration": true},
				"textDocument": map[string]any{
					"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
					"hover":          map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				},
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
	if method == "textDocument/didOpen" || method == "textDocument/didChange" {
		// A previous diagnostic report is not proof about edited source.
		// Invalidate before sending so a rapid server notification cannot
		// be accidentally erased after it arrives.
		if body, ok := params.(map[string]any); ok {
			switch document := body["textDocument"].(type) {
			case protocol.TextDocumentItem:
				s.noteDocumentVersion(document.URI, document.Version)
			case protocol.VersionedTextDocumentIdentifier:
				s.noteDocumentVersion(document.URI, document.Version)
			}
		}
	}
	return t.Notify(method, params)
}
func (s *Session) signalDiagnosticsLocked() {
	if s.diagnosticEvent != nil {
		close(s.diagnosticEvent)
	}
	s.diagnosticEvent = make(chan struct{})
}
// noteDocumentVersion invalidates old diagnostics atomically with advancing
// the version. A concurrent older publish can never slip in between them.
func (s *Session) noteDocumentVersion(uri string, version int32) {
	s.mu.Lock()
	if s.documentVersions == nil {
		s.documentVersions = make(map[string]int32)
	}
	s.documentVersions[uri] = version
	delete(s.diagnostics, uri)
	s.signalDiagnosticsLocked()
	s.mu.Unlock()
}

func (s *Session) invalidateDiagnostics(uri string) {
	s.mu.Lock()
	delete(s.diagnostics, uri)
	s.signalDiagnosticsLocked()
	s.mu.Unlock()
}

// PullDiagnosticsKnownUnsupported avoids repeatedly probing a pull method
// rejected by this exact server incarnation. Reset on server restart.
func (s *Session) PullDiagnosticsKnownUnsupported() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pullDiagnosticsUnsupported
}
func (s *Session) MarkPullDiagnosticsUnsupported() {
	s.mu.Lock()
	s.pullDiagnosticsUnsupported = true
	s.mu.Unlock()
}

// WaitDiagnostics returns as soon as a report (including an empty report) is
// published. Earlier code always slept the whole fallback interval, even if
// the server had already delivered results. Timeout is an incomplete report,
// not an authoritative clean bill of health.
func (s *Session) WaitDiagnostics(ctx context.Context, uri string, wait time.Duration) ([]protocol.Diagnostic, bool, error) {
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		s.mu.RLock()
		items, ready := s.diagnostics[uri]
		event := s.diagnosticEvent
		copyOfItems := append([]protocol.Diagnostic(nil), items...)
		s.mu.RUnlock()
		if ready {
			return copyOfItems, true, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-timer.C:
			return nil, false, nil
		case <-event:
		}
	}
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
		// A language server may request multiple independently scoped settings.
		// Returning a single empty object silently discards configured analysis
		// options and violates the one-response-per-item LSP contract.
		var params struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		if err := json.Unmarshal(m.Params, &params); err != nil || params.Items == nil {
			return nil, &transport.ResponseError{Code: -32602, Message: "invalid workspace/configuration items"}
		}
		values := make([]any, len(params.Items))
		for i, item := range params.Items {
			values[i] = configurationSection(s.cfg.Settings, item.Section)
		}
		return values, nil
	case "workspace/workspaceFolders":
		return []map[string]string{{"uri": fileURI(s.root), "name": "workspace"}}, nil
	case "workspace/applyEdit":
		return map[string]any{"applied": false, "failureReason": "workspace edits are applied only by explicit MCP write tools"}, nil
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create":
		return map[string]any{}, nil
	default:
		return nil, &transport.ResponseError{Code: -32601, Message: "method not supported"}
	}
}
// configurationSection resolves the dotted section names used by LSP
// workspace/configuration. Missing settings are JSON null, not a fabricated
// empty object. A literal dotted key takes precedence over nested traversal.
func configurationSection(settings map[string]any, section string) any {
	if section == "" {
		if settings == nil {
			return map[string]any{}
		}
		return settings
	}
	if value, ok := settings[section]; ok {
		return value
	}
	var current any = settings
	for _, part := range strings.Split(section, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		value, found := object[part]
		if !found {
			return nil
		}
		current = value
	}
	return current
}

func (s *Session) notification(m transport.Message) {
	if m.Method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string                `json:"uri"`
		Version     *int32                `json:"version"`
		Diagnostics []protocol.Diagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(m.Params, &p) == nil {
		s.mu.Lock()
		if current, known := s.documentVersions[p.URI]; known && p.Version != nil && *p.Version != current {
			// LSP 3.17 allows an optional version on push diagnostics.
			// Older results must not wake WaitDiagnostics or overwrite the
			// report for the latest document revision. Unversioned servers
			// remain supported as a best-effort fallback.
			s.mu.Unlock()
			return
		}
		s.diagnostics[p.URI] = p.Diagnostics
		s.signalDiagnosticsLocked()
		s.mu.Unlock()
	}
}
func fileURI(path string) string { return normalize.FileURI(path) }
func clientCapabilities() map[string]any {
	return map[string]any{
		"workspace": map[string]any{
			"workspaceFolders":       true,
			"didChangeConfiguration": map[string]any{"dynamicRegistration": false},
			"symbol":                 map[string]any{"dynamicRegistration": false},
			"diagnostics":            map[string]any{},
			"workspaceEdit": map[string]any{
				"documentChanges": true,
			},
		},
		"textDocument": map[string]any{
			"synchronization":    map[string]any{"didSave": true},
			"publishDiagnostics": map[string]any{"relatedInformation": true, "versionSupport": true},
			"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
			"hover":              map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			"definition":         map[string]any{"linkSupport": true},
			"references":         map[string]any{},
			"implementation":     map[string]any{"linkSupport": true},
			"typeDefinition":     map[string]any{"linkSupport": true},
			"declaration":        map[string]any{"linkSupport": true},
			"callHierarchy":      map[string]any{},
			"typeHierarchy":      map[string]any{},
			"diagnostic":         map[string]any{},
			"rename": map[string]any{
				"dynamicRegistration": false,
				"prepareSupport":      true,
			},
			"formatting":      map[string]any{"dynamicRegistration": false},
			"rangeFormatting": map[string]any{"dynamicRegistration": false},
			"codeAction": map[string]any{
				"dynamicRegistration": false,
				"resolveSupport":      map[string]any{"properties": []string{"edit"}},
			},
		},
	}
}
func decodeCaps(m map[string]json.RawMessage, encoding string) protocol.Capabilities {
	has := func(k string) bool {
		b := m[k]
		return len(b) > 0 && string(b) != "false" && string(b) != "null"
	}
	rename, prepareRename := false, false
	if raw := m["renameProvider"]; len(raw) > 0 && string(raw) != "false" && string(raw) != "null" {
		rename = true
		if string(raw) != "true" {
			var options struct {
				PrepareProvider bool `json:"prepareProvider"`
			}
			if json.Unmarshal(raw, &options) == nil {
				prepareRename = options.PrepareProvider
			}
		}
	}
	codeAction, codeActionResolve := false, false
	if raw := m["codeActionProvider"]; len(raw) > 0 && string(raw) != "false" && string(raw) != "null" {
		codeAction = true
		if string(raw) != "true" {
			var options struct {
				ResolveProvider bool `json:"resolveProvider"`
			}
			if json.Unmarshal(raw, &options) == nil {
				codeActionResolve = options.ResolveProvider
			}
		}
	}
	return protocol.Capabilities{
		PositionEncoding: encoding, WorkspaceSymbol: has("workspaceSymbolProvider"), DocumentSymbol: has("documentSymbolProvider"),
		Hover: has("hoverProvider"), Definition: has("definitionProvider"), References: has("referencesProvider"),
		Implementation: has("implementationProvider"), TypeDefinition: has("typeDefinitionProvider"), Declaration: has("declarationProvider"),
		CallHierarchy: has("callHierarchyProvider"), TypeHierarchy: has("typeHierarchyProvider"), Diagnostics: has("diagnosticProvider"),
		WorkspaceDiagnostics: has("diagnosticProvider"), Rename: rename, PrepareRename: prepareRename,
		Formatting: has("documentFormattingProvider"), RangeFormatting: has("documentRangeFormattingProvider"),
		CodeAction: codeAction, CodeActionResolve: codeActionResolve,
	}
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

// Configured reports whether at least one server is configured for a profile.
func (m *Manager) Configured(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers[key]) > 0
}

// ForAll returns every configured server for a language, including separate
// monorepo roots. Workspace-wide symbol lookups must not query only the
// default/first server and silently omit the other projects.
func (m *Manager) ForAll(key string) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	servers := m.servers[key]
	if len(servers) == 0 {
		return nil, core.NewError(core.UnsupportedLanguage, "no language server profile")
	}
	sessions := make([]*Session, 0, len(servers))
	for i, server := range servers {
		sessions = append(sessions, m.forServer(key, server, i))
	}
	return sessions, nil
}

func (m *Manager) ForPath(key string, targetPath string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	servers := m.servers[key]
	if len(servers) == 0 {
		return nil, core.NewError(core.UnsupportedLanguage, "no language server profile")
	}
	chosen := config.SelectServer(servers, targetPath)
	for i, server := range servers {
		if reflect.DeepEqual(server, chosen) {
			return m.forServer(key, server, i), nil
		}
	}
	return nil, core.NewError(core.InternalError, "selected server is not configured")
}

// forServer must be called with m.mu held. An index is part of the key so
// profiles with two servers under one directory never share the wrong LSP.
func (m *Manager) forServer(key string, srv config.Server, index int) *Session {
	cacheKey := fmt.Sprintf("%s:%d:%s", key, index, srv.Directory)
	if s := m.sessions[cacheKey]; s != nil {
		return s
	}
	root := m.root
	if srv.Directory != "" && srv.Directory != "." {
		if filepath.IsAbs(srv.Directory) {
			root = srv.Directory
		} else {
			root = filepath.Join(m.root, srv.Directory)
		}
	}
	s := New(key, root, srv)
	m.sessions[cacheKey] = s
	return s
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
