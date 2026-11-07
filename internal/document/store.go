package document

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"reflect"
	"sync"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

// Notifier sends document lifecycle messages to an LSP server.
type Notifier interface {
	Notify(method string, params any) error
}

// Document is the versioned file state known by an LSP session.
type Document struct {
	Path, URI, LanguageID, Hash string
	Version                     int32
	Text                        []byte
	Opened                      bool
}

// Store tracks files that have been opened in an LSP session.
type documentState struct {
	value      Document
	generation uint64
}

type Store struct {
	mu   sync.Mutex
	// Document-open state belongs to an individual LSP connection, not
	// globally to a path. Two sessions may legitimately open the same file.
	docs map[Notifier]map[string]*documentState
}

// NewStore creates an empty document store.
func NewStore() *Store {
	return &Store{docs: map[Notifier]map[string]*documentState{}}
}

// Sync opens a document once per LSP connection and sends changes only when
// source bytes change. A restarted server has a new generation and must
// receive didOpen again even if the file's content is identical.
func (s *Store) Sync(ctx context.Context, n Notifier, path, languageID string) (Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Document{}, err
	}
	return s.SyncBytes(ctx, n, path, languageID, b)
}

// SyncBytes sends the caller's already-read source snapshot to the LSP.
// Project discovery has just read each candidate to search for its symbol;
// rereading it here doubles disk IO without adding semantic information.
// The caller transfers ownership of b and must not mutate it after this call.
func (s *Store) SyncBytes(_ context.Context, n Notifier, path, languageID string, b []byte) (Document, error) {
	if n == nil || !reflect.TypeOf(n).Comparable() {
		return Document{}, core.NewError(core.InvalidArgument, "document notifier must have a comparable identity")
	}
	generation := uint64(0)
	if versioned, ok := n.(interface{ Generation() uint64 }); ok {
		generation = versioned.Generation()
	}

	// Project warm-up reuses an immutable source buffer from its bounded
	// file index. If the exact buffer is already open in the same generation,
	// it was validated and hashed on the first sync. Avoid validating and
	// hashing the same bytes for every reference/rename query.
	// A server restart (new generation) always goes through didOpen below.
	s.mu.Lock()
	if state := s.docs[n][path]; state != nil &&
		state.generation == generation &&
		state.value.LanguageID == languageID &&
		len(state.value.Text) == len(b) &&
		(len(b) == 0 || &state.value.Text[0] == &b[0]) {
		d := state.value
		s.mu.Unlock()
		return d, nil
	}
	s.mu.Unlock()

	if !utf8.Valid(b) {
		return Document{}, core.NewError(core.InvalidArgument, "file is not valid UTF-8")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(b))
	uri := fileURI(path)

	s.mu.Lock()
	defer s.mu.Unlock()
	sessionDocs := s.docs[n]
	if sessionDocs == nil {
		sessionDocs = make(map[string]*documentState)
		s.docs[n] = sessionDocs
	}
	state := sessionDocs[path]
	if state == nil || state.generation != generation {
		// A didOpen must be delivered to the new server, regardless of
		// content hash. Never commit local state after a failed notify.
		item := protocol.TextDocumentItem{URI: uri, LanguageID: languageID, Version: 1, Text: string(b)}
		if err := n.Notify("textDocument/didOpen", map[string]any{"textDocument": item}); err != nil {
			return Document{}, err
		}
		d := Document{Path: path, URI: uri, LanguageID: languageID, Version: 1, Text: b, Hash: hash, Opened: true}
		sessionDocs[path] = &documentState{value: d, generation: generation}
		return d, nil
	}
	if state.value.LanguageID != languageID {
		return Document{}, core.NewError(core.InvalidArgument, "document language changed within a language-server session")
	}
	if state.value.Hash != hash {
		version := state.value.Version + 1
		// Do not mutate Version before the transport accepts the message.
		if err := n.Notify("textDocument/didChange", map[string]any{
			"textDocument": protocol.VersionedTextDocumentIdentifier{URI: uri, Version: version},
			"contentChanges": []map[string]string{{"text": string(b)}},
		}); err != nil {
			return Document{}, err
		}
		state.value.Version = version
		state.value.Text = b
		state.value.Hash = hash
	}
	return state.value, nil
}

func fileURI(path string) string {
	return normalize.FileURI(path)
}
