package document

import (
	"bytes"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"strings"
	"unicode/utf8"
)

func ToLSP(text []byte, p core.Position, encoding string) (protocol.Position, error) {
	if p.Line < 1 || p.Column < 1 {
		return protocol.Position{}, core.NewError(core.InvalidArgument, "position must be one-based")
	}
	line, ok := sourceLine(text, p.Line-1)
	if !ok {
		return protocol.Position{}, core.NewError(core.InvalidArgument, "line out of range")
	}
	offset, err := unitsAtCodePoint(line, p.Column-1, encoding)
	if err != nil {
		return protocol.Position{}, err
	}
	return protocol.Position{Line: p.Line - 1, Character: offset}, nil
}
func FromLSP(text []byte, p protocol.Position, encoding string) (core.Position, error) {
	point, _, err := FromLSPWithLine(text, p, encoding)
	return point, err
}

// FromLSPWithLine returns the validated code-point position and its source
// line in one scan. Cursor-based semantic warm-up needs both; asking for them
// separately would traverse the prefix twice or split the entire document.
func FromLSPWithLine(text []byte, p protocol.Position, encoding string) (core.Position, string, error) {
	line, ok := sourceLine(text, p.Line)
	if !ok || p.Character < 0 {
		return core.Position{}, "", core.NewError(core.InvalidArgument, "position out of range")
	}
	cp, err := codePointAtUnits(line, p.Character, encoding)
	if err != nil {
		return core.Position{}, "", err
	}
	return core.Position{Line: p.Line + 1, Column: cp + 1}, line, nil
}
// sourceLine selects one zero-based source line without copying and splitting
// the entire document. In large generated files, strings.Split allocated an
// O(file-size) string and an O(line-count) slice for every position conversion.
// A trailing newline still denotes a valid final empty line, as in Split.
func sourceLine(text []byte, line int) (string, bool) {
	if line < 0 {
		return "", false
	}
	start := 0
	for ; line > 0; line-- {
		newline := bytes.IndexByte(text[start:], '\n')
		if newline < 0 {
			return "", false
		}
		start += newline + 1
	}
	end := bytes.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += start
	}
	return strings.TrimSuffix(string(text[start:end]), "\r"), true
}

func unitsAtCodePoint(s string, target int, encoding string) (int, error) {
	cp, u := 0, 0
	for _, r := range s {
		if cp == target {
			return u, nil
		}
		cp++
		switch encoding {
		case "utf-8":
			u += utf8.RuneLen(r)
		case "utf-32":
			u++
		default:
			if r > 0xffff {
				u += 2
			} else {
				u++
			}
		}
	}
	if cp == target {
		return u, nil
	}
	return 0, core.NewError(core.InvalidArgument, "column out of range")
}
func codePointAtUnits(s string, target int, encoding string) (int, error) {
	cp, u := 0, 0
	for _, r := range s {
		if u == target {
			return cp, nil
		}
		step := 1
		switch encoding {
		case "utf-8":
			step = utf8.RuneLen(r)
		case "utf-32":
			step = 1
		default:
			if r > 0xffff {
				step = 2
			}
		}
		if u+step > target {
			return 0, core.NewError(core.InvalidArgument, "column splits code point")
		}
		u += step
		cp++
	}
	if u == target {
		return cp, nil
	}
	return 0, core.NewError(core.InvalidArgument, "column out of range")
}
