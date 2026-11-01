package tools

import (
	"bytes"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// JavaScript/TypeScript identifiers can spell any identifier character with
// \uXXXX or \u{...}. A literal-byte search for "id" misses references such as
// i\u0064 and \u{69}d, so a project-wide rename can silently omit files.
// This decoder is deliberately lexical: decoding escapes inside strings and
// comments may add candidates, but the language server decides symbol identity.
func warmDecodeJSEscapes(content []byte) []byte {
	if !bytes.Contains(content, []byte(`\u`)) {
		return content
	}
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); {
		if content[i] == '\\' {
			if r, n, ok := warmReadJSEscape(content[i:]); ok {
				out = utf8.AppendRune(out, r)
				i += n
				continue
			}
		}
		out = append(out, content[i])
		i++
	}
	return out
}

// warmReadJSEscape returns a decoded Unicode scalar and the number of ASCII
// source bytes consumed. It also supports a pair of UTF-16 surrogate escapes.
func warmReadJSEscape(data []byte) (rune, int, bool) {
	if len(data) < 4 || data[0] != '\\' || data[1] != 'u' {
		return 0, 0, false
	}
	if data[2] == '{' {
		end := bytes.IndexByte(data[3:], '}')
		if end < 1 || end > 6 {
			return 0, 0, false
		}
		value, err := strconv.ParseUint(string(data[3:3+end]), 16, 32)
		if err != nil || value > utf8.MaxRune || value >= 0xd800 && value <= 0xdfff {
			return 0, 0, false
		}
		return rune(value), end + 4, true
	}
	if len(data) < 6 {
		return 0, 0, false
	}
	value, err := strconv.ParseUint(string(data[2:6]), 16, 16)
	if err != nil {
		return 0, 0, false
	}
	if value >= 0xd800 && value <= 0xdbff {
		if len(data) < 12 || data[6] != '\\' || data[7] != 'u' {
			return 0, 0, false
		}
		low, err := strconv.ParseUint(string(data[8:12]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return 0, 0, false
		}
		return utf16.DecodeRune(rune(value), rune(low)), 12, true
	}
	if value >= 0xdc00 && value <= 0xdfff {
		return 0, 0, false
	}
	return rune(value), 6, true
}

// Convert the source line and cursor together. A cursor anywhere inside an
// escaped identifier character must still resolve to that decoded character.
// Both the original and decoded offsets are rune indices, not UTF-16 units.
func warmDecodeLinePosition(line []rune, offset int) ([]rune, int) {
	if !bytes.Contains([]byte(string(line)), []byte(`\u`)) {
		return line, offset
	}
	out := make([]rune, 0, len(line))
	mapped := offset
	for i := 0; i < len(line); {
		if line[i] == '\\' {
			// Escape sequences are at most 12 ASCII bytes (surrogate pair).
			// Never convert the entire remaining line for every backslash.
			end := i + 12
			if end > len(line) {
				end = len(line)
			}
			if r, n, ok := warmReadJSEscape([]byte(string(line[i:end]))); ok {
				if offset >= i && offset < i+n {
					mapped = len(out)
				}
				out = append(out, r)
				i += n
				continue
			}
		}
		if offset == i {
			mapped = len(out)
		}
		out = append(out, line[i])
		i++
	}
	if offset == len(line) {
		mapped = len(out)
	}
	return out, mapped
}
