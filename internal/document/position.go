package document

import (
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"strings"
	"unicode/utf8"
)

func ToLSP(text []byte, p core.Position, encoding string) (protocol.Position, error) {
	if p.Line < 1 || p.Column < 1 {
		return protocol.Position{}, core.NewError(core.InvalidArgument, "position must be one-based")
	}
	lines := strings.Split(string(text), "\n")
	if p.Line > len(lines) {
		return protocol.Position{}, core.NewError(core.InvalidArgument, "line out of range")
	}
	line := strings.TrimSuffix(lines[p.Line-1], "\r")
	offset, err := unitsAtCodePoint(line, p.Column-1, encoding)
	if err != nil {
		return protocol.Position{}, err
	}
	return protocol.Position{Line: p.Line - 1, Character: offset}, nil
}
func FromLSP(text []byte, p protocol.Position, encoding string) (core.Position, error) {
	lines := strings.Split(string(text), "\n")
	if p.Line < 0 || p.Line >= len(lines) || p.Character < 0 {
		return core.Position{}, core.NewError(core.InvalidArgument, "position out of range")
	}
	line := strings.TrimSuffix(lines[p.Line], "\r")
	cp, err := codePointAtUnits(line, p.Character, encoding)
	if err != nil {
		return core.Position{}, err
	}
	return core.Position{Line: p.Line + 1, Column: cp + 1}, nil
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
