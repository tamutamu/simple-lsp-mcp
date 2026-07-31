package document

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"
)

type Notifier interface {
	Notify(method string, params any) error
}
type Document struct {
	Path, URI, LanguageID, Hash string
	Version                     int32
	Text                        []byte
	Opened                      bool
}
type Store struct {
	mu   sync.Mutex
	docs map[string]*Document
}

func NewStore() *Store { return &Store{docs: map[string]*Document{}} }
func (s *Store) Sync(_ context.Context, n Notifier, path, languageID string) (Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Document{}, err
	}
	if !utf8.Valid(b) {
		return Document{}, core.NewError(core.InvalidArgument, "file is not valid UTF-8")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(b))
	uri := fileURI(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[path]
	if d == nil {
		d = &Document{Path: path, URI: uri, LanguageID: languageID, Version: 1, Text: b, Hash: hash}
		if err := n.Notify("textDocument/didOpen", map[string]any{"textDocument": protocol.TextDocumentItem{URI: uri, LanguageID: languageID, Version: 1, Text: string(b)}}); err != nil {
			return Document{}, err
		}
		d.Opened = true
		s.docs[path] = d
	} else if d.Hash != hash {
		d.Version++
		if err := n.Notify("textDocument/didChange", map[string]any{"textDocument": protocol.VersionedTextDocumentIdentifier{URI: d.URI, Version: d.Version}, "contentChanges": []map[string]string{{"text": string(b)}}}); err != nil {
			return Document{}, err
		}
		d.Text = b
		d.Hash = hash
	}
	return *d, nil
}
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
