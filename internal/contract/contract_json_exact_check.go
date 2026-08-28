package contract

import (
	"bytes"
	"unicode/utf8"
)

// maxCanonicalExactJSONCheckDepth mirrors encoding/json's decoder nesting
// limit. Deeper documents fall back to the full canonicalization pass, which
// reports the decoder's own depth error.
const maxCanonicalExactJSONCheckDepth = 10_000

// isCanonicalExactJSON reports whether data already holds exactly one value in
// the byte form canonicalizeExactJSON emits: no whitespace, object members in
// strictly increasing UTF-16 key order, canonical number spellings, and
// canonical string escaping. The check is conservative — any construct it
// cannot cheaply prove canonical (escaped keys, non-ASCII keys, unusual
// escapes, deep nesting) reports false so the full decode/re-encode pass
// stays the single source of truth for wire bytes and error behavior.
func isCanonicalExactJSON(data []byte) bool {
	offset, ok := scanCanonicalExactJSONValue(data, 0, 0)
	return ok && offset == len(data)
}

func scanCanonicalExactJSONValue(data []byte, offset, depth int) (int, bool) {
	if depth > maxCanonicalExactJSONCheckDepth || offset >= len(data) {
		return 0, false
	}
	switch data[offset] {
	case '{':
		return scanCanonicalExactJSONObject(data, offset, depth)
	case '[':
		return scanCanonicalExactJSONArray(data, offset, depth)
	case '"':
		next, _, ok := scanCanonicalExactJSONString(data, offset)
		return next, ok
	case 't':
		return scanCanonicalExactJSONLiteral(data, offset, "true")
	case 'f':
		return scanCanonicalExactJSONLiteral(data, offset, "false")
	case 'n':
		return scanCanonicalExactJSONLiteral(data, offset, "null")
	default:
		return scanCanonicalExactJSONNumber(data, offset)
	}
}

func scanCanonicalExactJSONLiteral(data []byte, offset int, literal string) (int, bool) {
	if len(data)-offset < len(literal) || string(data[offset:offset+len(literal)]) != literal {
		return 0, false
	}
	return offset + len(literal), true
}

func scanCanonicalExactJSONObject(data []byte, offset, depth int) (int, bool) {
	offset++
	if offset < len(data) && data[offset] == '}' {
		return offset + 1, true
	}
	havePrevious := false
	var previousKey []byte
	for {
		next, plainASCII, ok := scanCanonicalExactJSONString(data, offset)
		if !ok || !plainASCII {
			// Keys with escapes or non-ASCII characters need decoded UTF-16
			// ordering; leave them to the full canonicalization pass.
			return 0, false
		}
		key := data[offset+1 : next-1]
		if havePrevious && bytes.Compare(previousKey, key) >= 0 {
			return 0, false
		}
		havePrevious, previousKey = true, key
		offset = next
		if offset >= len(data) || data[offset] != ':' {
			return 0, false
		}
		offset++
		offset, ok = scanCanonicalExactJSONValue(data, offset, depth+1)
		if !ok || offset >= len(data) {
			return 0, false
		}
		switch data[offset] {
		case ',':
			offset++
		case '}':
			return offset + 1, true
		default:
			return 0, false
		}
	}
}

func scanCanonicalExactJSONArray(data []byte, offset, depth int) (int, bool) {
	offset++
	if offset < len(data) && data[offset] == ']' {
		return offset + 1, true
	}
	for {
		var ok bool
		offset, ok = scanCanonicalExactJSONValue(data, offset, depth+1)
		if !ok || offset >= len(data) {
			return 0, false
		}
		switch data[offset] {
		case ',':
			offset++
		case ']':
			return offset + 1, true
		default:
			return 0, false
		}
	}
}

// scanCanonicalExactJSONString accepts exactly the string encoding
// writeExactCanonicalJSON produces: encoding/json escaping with HTML escapes
// kept and U+2028/U+2029 written as raw characters. plainASCII reports that
// the content carries no escapes and no bytes above 0x7f, so the raw content
// bytes order identically to the decoded string under UTF-16 comparison.
func scanCanonicalExactJSONString(data []byte, offset int) (next int, plainASCII bool, ok bool) {
	if offset >= len(data) || data[offset] != '"' {
		return 0, false, false
	}
	offset++
	plainASCII = true
	for offset < len(data) {
		character := data[offset]
		switch {
		case character == '"':
			return offset + 1, plainASCII, true
		case character == '\\':
			plainASCII = false
			offset++
			if offset >= len(data) {
				return 0, false, false
			}
			switch data[offset] {
			case '\\':
				// The full pass rewrites the byte sequence \u2028 (and
				// \u2029) after re-encoding without seeing the preceding
				// escape, so a literal backslash followed by the text u2028
				// or u2029 does not survive it unchanged. Leave those
				// strings to the full pass.
				if remainder := data[offset+1:]; len(remainder) >= 5 && remainder[0] == 'u' && remainder[1] == '2' && remainder[2] == '0' && remainder[3] == '2' && (remainder[4] == '8' || remainder[4] == '9') {
					return 0, false, false
				}
				offset++
			case '"', 'b', 'f', 'n', 'r', 't':
				offset++
			case 'u':
				if !canonicalExactJSONUnicodeEscape(data[offset+1:]) {
					return 0, false, false
				}
				offset += 5
			default:
				return 0, false, false
			}
		case character < 0x20, character == '<', character == '>', character == '&':
			// Control characters and HTML-significant characters are always
			// escaped in canonical output.
			return 0, false, false
		case character < 0x80:
			offset++
		default:
			decoded, size := utf8.DecodeRune(data[offset:])
			if decoded == utf8.RuneError && size <= 1 {
				return 0, false, false
			}
			plainASCII = false
			offset += size
		}
	}
	return 0, false, false
}

// canonicalExactJSONUnicodeEscape accepts the exact \u escapes encoding/json
// emits: \u003c, \u003e, \u0026, and \u00xx lowercase-hex control characters
// other than \b, \t, \n, \f, and \r (which canonical output spells as short
// escapes).
func canonicalExactJSONUnicodeEscape(data []byte) bool {
	if len(data) < 4 || data[0] != '0' || data[1] != '0' {
		return false
	}
	switch {
	case data[2] == '3' && (data[3] == 'c' || data[3] == 'e'):
		return true
	case data[2] == '2' && data[3] == '6':
		return true
	case data[2] == '0' || data[2] == '1':
		value := byte(0)
		switch {
		case data[3] >= '0' && data[3] <= '9':
			value = data[3] - '0'
		case data[3] >= 'a' && data[3] <= 'f':
			value = data[3] - 'a' + 10
		default:
			return false
		}
		if data[2] == '1' {
			value += 0x10
		}
		switch value {
		case '\b', '\t', '\n', '\f', '\r':
			return false
		default:
			return true
		}
	default:
		return false
	}
}

// scanCanonicalExactJSONNumber accepts the exact spellings
// normalizeExactJSONNumber emits: no exponent, no leading zeros, no trailing
// fraction zeros, and no negative zero.
func scanCanonicalExactJSONNumber(data []byte, offset int) (int, bool) {
	negative := false
	if offset < len(data) && data[offset] == '-' {
		negative = true
		offset++
	}
	if offset >= len(data) || data[offset] < '0' || data[offset] > '9' {
		return 0, false
	}
	integerIsZero := false
	if data[offset] == '0' {
		integerIsZero = true
		offset++
	} else {
		for offset < len(data) && data[offset] >= '0' && data[offset] <= '9' {
			offset++
		}
	}
	hasFraction := false
	if offset < len(data) && data[offset] == '.' {
		hasFraction = true
		offset++
		fractionStart := offset
		for offset < len(data) && data[offset] >= '0' && data[offset] <= '9' {
			offset++
		}
		if offset == fractionStart || data[offset-1] == '0' {
			return 0, false
		}
	}
	if offset < len(data) && (data[offset] == 'e' || data[offset] == 'E') {
		return 0, false
	}
	if negative && integerIsZero && !hasFraction {
		return 0, false
	}
	return offset, true
}
