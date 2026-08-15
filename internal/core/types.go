// Package core defines protocol-neutral values shared by tool handlers.
package core

import "fmt"

type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}
type Target struct {
	SymbolID string `json:"symbol_id,omitempty"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}
type Meta struct {
	Complete        bool     `json:"complete"`
	Truncated       bool     `json:"truncated"`
	Warnings        []string `json:"warnings,omitempty"`
	Servers         []string `json:"servers,omitempty"`
	SourceTruncated bool     `json:"source_truncated,omitempty"`
}
type SymbolSummary struct {
	SymbolID       string `json:"symbol_id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	ContainerName  string `json:"container_name,omitempty"`
	Language       string `json:"language"`
	Path           string `json:"path"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selection_range"`
}
type Location struct {
	Path          string `json:"path"`
	Range         Range  `json:"range"`
	SymbolID      string `json:"symbol_id,omitempty"`
	SymbolName    string `json:"symbol_name,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	Preview       string `json:"preview,omitempty"`
}
type Diagnostic struct {
	Path      string     `json:"path"`
	Range     Range      `json:"range"`
	Severity  string     `json:"severity"`
	Code      any        `json:"code,omitempty"`
	Source    string     `json:"source,omitempty"`
	Message   string     `json:"message"`
	Locations []Location `json:"locations,omitempty"`
}

func (t Target) Validate() error {
	byID := t.SymbolID != ""
	byPosition := t.Path != "" || t.Line != 0 || t.Column != 0
	if byID == byPosition || (!byID && !byPosition) {
		return NewError(InvalidArgument, "specify exactly one target form")
	}
	if byPosition && (t.Path == "" || t.Line < 1 || t.Column < 1) {
		return NewError(InvalidArgument, "path, line, and column must be specified")
	}
	return nil
}

type ErrorCode string

const (
	InvalidArgument           ErrorCode = "INVALID_ARGUMENT"
	InvalidPath               ErrorCode = "INVALID_PATH"
	UnsupportedLanguage       ErrorCode = "UNSUPPORTED_LANGUAGE"
	LanguageServerNotFound    ErrorCode = "LANGUAGE_SERVER_NOT_FOUND"
	LanguageServerStartFailed ErrorCode = "LANGUAGE_SERVER_START_FAILED"
	MethodNotSupported        ErrorCode = "METHOD_NOT_SUPPORTED"
	SymbolNotFound            ErrorCode = "SYMBOL_NOT_FOUND"
	AmbiguousSymbol           ErrorCode = "AMBIGUOUS_SYMBOL"
	StaleSymbol               ErrorCode = "STALE_SYMBOL"
	RequestTimeout            ErrorCode = "REQUEST_TIMEOUT"
	LSPServerCrashed          ErrorCode = "LSP_SERVER_CRASHED"
	InternalError             ErrorCode = "INTERNAL_ERROR"
)

type AppError struct {
	Code     ErrorCode `json:"code"`
	Message  string    `json:"message"`
	Language string    `json:"language,omitempty"`
	Method   string    `json:"method,omitempty"`
	Cause    error
}

func (e *AppError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func (e *AppError) Unwrap() error { return e.Cause }
func NewError(code ErrorCode, message string) *AppError {
	return &AppError{Code: code, Message: message}
}
func WithCause(code ErrorCode, message string, cause error) *AppError {
	if cause != nil {
		message = fmt.Sprintf("%s: %v", message, cause)
	}
	return &AppError{Code: code, Message: message, Cause: cause}
}

func ClampLimit(limit, max, fallback int) (int, error) {
	if limit == 0 {
		return fallback, nil
	}
	if limit < 1 {
		return 0, NewError(InvalidArgument, "limit must be positive")
	}
	if limit > max {
		return max, nil
	}
	return limit, nil
}
