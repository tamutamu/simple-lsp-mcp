package tools

import (
	"bytes"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// editPositionIndex converts many LSP positions in one source without
// splitting and rescanning the entire file for every location or edit.
// It is immutable and scoped to the exact source bytes of one tool request.
const (
	// Keep dense indexing for ordinary sources. A generated file with tens
	// of millions of short lines must not allocate hundreds of MiB of offsets.
	maxDenseLineStarts = 1 << 18
	sparseLineStride   = 32
)

type editPositionIndex struct {
	text       []byte
	lineStarts []int
	lineCount  int
	sparse     bool
}

func newEditPositionIndex(text []byte) *editPositionIndex {
	idx := &editPositionIndex{text: text, lineStarts: []int{0}, lineCount: 1}
	for i, b := range text {
		if b != '\n' {
			continue
		}
		idx.lineCount++
		if !idx.sparse {
			idx.lineStarts = append(idx.lineStarts, i+1)
			if len(idx.lineStarts) > maxDenseLineStarts {
				compact := make([]int, 0, len(idx.lineStarts)/sparseLineStride+1)
				for j := 0; j < len(idx.lineStarts); j += sparseLineStride {
					compact = append(compact, idx.lineStarts[j])
				}
				idx.lineStarts = compact
				idx.sparse = true
			}
		} else if (idx.lineCount-1)%sparseLineStride == 0 {
			idx.lineStarts = append(idx.lineStarts, i+1)
		}
	}
	return idx
}

func (idx *editPositionIndex) lineBounds(line int) (int, int) {
	if !idx.sparse {
		start := idx.lineStarts[line]
		if line+1 < idx.lineCount {
			return start, idx.lineStarts[line+1] - 1
		}
		return start, len(idx.text)
	}
	block := line / sparseLineStride
	start := idx.lineStarts[block]
	for cursor := block * sparseLineStride; cursor < line; cursor++ {
		start += bytes.IndexByte(idx.text[start:], '\n') + 1
	}
	end := len(idx.text)
	if next := bytes.IndexByte(idx.text[start:], '\n'); next >= 0 {
		end = start + next
	}
	return start, end
}

// point returns both the public one-based code-point position and its byte
// offset. CRLF, UTF-8/16/32 and split-code-point errors match document.FromLSP.
func (idx *editPositionIndex) point(p protocol.Position, encoding string) (core.Position, int, error) {
	if p.Line < 0 || p.Line >= idx.lineCount || p.Character < 0 {
		return core.Position{}, 0, core.NewError(core.InvalidArgument, "position out of range")
	}
	start, end := idx.lineBounds(p.Line)
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
