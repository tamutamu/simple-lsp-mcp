package tools

import (
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// editPositionIndex converts many LSP positions in one source without
// splitting and rescanning the entire file for every location or edit.
// It is immutable and scoped to the exact source bytes of one tool request.
type editPositionIndex struct {
	text       []byte
	lineStarts []int
}

func newEditPositionIndex(text []byte) *editPositionIndex {
	starts := []int{0}
	for i, b := range text {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &editPositionIndex{text: text, lineStarts: starts}
}

// point returns both the public one-based code-point position and its byte
// offset. CRLF, UTF-8/16/32 and split-code-point errors match document.FromLSP.
func (idx *editPositionIndex) point(p protocol.Position, encoding string) (core.Position, int, error) {
	if p.Line < 0 || p.Line >= len(idx.lineStarts) || p.Character < 0 {
		return core.Position{}, 0, core.NewError(core.InvalidArgument, "position out of range")
	}
	start := idx.lineStarts[p.Line]
	end := len(idx.text)
	if p.Line+1 < len(idx.lineStarts) {
		end = idx.lineStarts[p.Line+1] - 1
	}
	if end > start && idx.text[end-1] == '\r' {
		end--
	}
	units, cp, offset := 0, 0, start
	for offset < end {
		if units == p.Character {
			return core.Position{Line: p.Line + 1, Column: cp + 1}, offset, nil
		}
		r, size := utf8.DecodeRune(idx.text[offset:end])
		step := 1
		switch encoding {
		case "utf-8":
			step = size
		case "utf-32":
			// One UTF-32 code unit per code point.
		default:
			if r > 0xffff {
				step = 2
			}
		}
		if units+step > p.Character {
			return core.Position{}, 0, core.NewError(core.InvalidArgument, "column splits code point")
		}
		units += step
		cp++
		offset += size
	}
	if units == p.Character {
		return core.Position{Line: p.Line + 1, Column: cp + 1}, offset, nil
	}
	return core.Position{}, 0, core.NewError(core.InvalidArgument, "column out of range")
}

func (idx *editPositionIndex) rangeAt(r protocol.Range, encoding string) (core.Range, int, int, error) {
	start, a, err := idx.point(r.Start, encoding)
	if err != nil {
		return core.Range{}, 0, 0, err
	}
	end, z, err := idx.point(r.End, encoding)
	if err != nil {
		return core.Range{}, 0, 0, err
	}
	return core.Range{Start: start, End: end}, a, z, nil
}
