package protocol

import "encoding/json"

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int32  `json:"version"`
}
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int32  `json:"version"`
	Text       string `json:"text"`
}
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}
type OptionalVersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int32 `json:"version,omitempty"`
}
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []json.RawMessage     `json:"documentChanges,omitempty"`
}
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}
type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}
type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	ContainerName string   `json:"containerName,omitempty"`
	Location      Location `json:"location"`
}
type WorkspaceSymbol struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	ContainerName string   `json:"containerName,omitempty"`
	Location      Location `json:"location"`
}
type Diagnostic struct {
	Range              Range                          `json:"range"`
	Severity           *int                           `json:"severity,omitempty"`
	Code               any                            `json:"code,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
}
type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}
type CallHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Detail         string `json:"detail,omitempty"`
	Data           any    `json:"data,omitempty"`
}
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// Hover is the textDocument/hover response. Contents is one of
// MarkupContent, MarkedString, or MarkedString[], and is decoded by the
// caller since the three shapes are otherwise ambiguous to unmarshal.
type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// MarkupContent is the LSP 3.x hover body.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// MarkedString is the deprecated hover body some servers still emit.
type MarkedString struct {
	Language string `json:"language"`
	Value    string `json:"value"`
}
type TypeHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Detail         string `json:"detail,omitempty"`
	Data           any    `json:"data,omitempty"`
}
type Capabilities struct {
	PositionEncoding     string
	WorkspaceSymbol      bool
	DocumentSymbol       bool
	Hover                bool
	Definition           bool
	References           bool
	Implementation       bool
	TypeDefinition       bool
	Declaration          bool
	CallHierarchy        bool
	TypeHierarchy        bool
	Diagnostics          bool
	WorkspaceDiagnostics bool
	Rename               bool
	PrepareRename        bool
	Formatting           bool
}
