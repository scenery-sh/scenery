package runtime

import (
	"strings"
	"unicode/utf8"
)

// Redact before folding whitespace: comments, escaped strings and PostgreSQL
// dollar strings can contain whitespace and quotes that change token boundaries.
// Only sqlc's stable query name is retained from comments. Arguments never enter
// this function; pgx supplies only their count to the runtime.
func normalizeDBQuery(query string) string {
	query = strings.TrimSpace(query)
	name := sqlcQueryNameRE.FindStringSubmatch(query)
	var out strings.Builder
	if len(name) == 2 {
		out.WriteString("-- name: " + name[1] + " ")
	}
	for i := 0; i < len(query); {
		switch {
		case strings.HasPrefix(query[i:], "--"):
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				i = len(query)
			} else {
				i += end + 1
			}
			out.WriteByte(' ')
		case strings.HasPrefix(query[i:], "/*"):
			i += 2
			depth := 1
			for i < len(query) && depth > 0 {
				switch {
				case strings.HasPrefix(query[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(query[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
			}
			out.WriteByte(' ')
		case query[i] == '\'':
			escaped := i > 0 && (query[i-1] == 'e' || query[i-1] == 'E') && (i == 1 || !isSQLIdentByte(query[i-2]))
			i = sqlQuotedEnd(query, i, '\'', escaped)
			out.WriteByte('?')
		case query[i] == '"':
			// Double quotes delimit identifiers, not values.
			end := sqlQuotedEnd(query, i, '"', false)
			out.WriteString(query[i:end])
			i = end
		case query[i] == '$':
			end := i + 1
			for end < len(query) && isSQLIdentByte(query[end]) {
				end++
			}
			if end < len(query) && query[end] == '$' && (end == i+1 || !isSQLDigit(query[i+1])) {
				delimiter := query[i : end+1]
				closeAt := strings.Index(query[end+1:], delimiter)
				if closeAt < 0 {
					i = len(query)
				} else {
					i = end + 1 + closeAt + len(delimiter)
				}
				out.WriteByte('?')
			} else {
				out.WriteString(query[i:end]) // $1, $12, etc.
				i = end
			}
		case isSQLDigit(query[i]) && (i == 0 || !isSQLIdentByte(query[i-1])):
			end := i + 1
			for end < len(query) {
				ch := query[end]
				if isSQLIdentByte(ch) || ch == '.' || ((ch == '+' || ch == '-') && (query[end-1] == 'e' || query[end-1] == 'E')) {
					end++
					continue
				}
				break
			}
			out.WriteByte('?')
			i = end
		default:
			out.WriteByte(query[i])
			i++
		}
	}
	normalized := strings.Join(strings.Fields(out.String()), " ")
	if normalized == "" {
		return "unknown"
	}
	if len(normalized) > maxDBQueryLength {
		end := maxDBQueryLength
		for !utf8.RuneStart(normalized[end]) {
			end--
		}
		return normalized[:end] + "..."
	}
	return normalized
}

func sqlQuotedEnd(query string, start int, quote byte, escapes bool) int {
	for i := start + 1; i < len(query); i++ {
		if escapes && query[i] == '\\' {
			i++
			continue
		}
		if query[i] == quote {
			if i+1 < len(query) && query[i+1] == quote {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(query)
}

func isSQLDigit(ch byte) bool { return ch >= '0' && ch <= '9' }

func isSQLIdentByte(ch byte) bool {
	return ch == '_' || ch >= utf8.RuneSelf || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || isSQLDigit(ch)
}
