package postgresdb

import (
	"fmt"
	"strings"
)

// ValidateMigrationSQL excludes transaction/session control from app-authored
// SQL. PostgreSQL remains the parser and transactional executor; this is not a
// sandbox for untrusted SQL. Literal bodies and nested comments are masked so
// their semicolons cannot turn COMMIT into an apparent separate statement.
func ValidateMigrationSQL(script string) error {
	if len(script) == 0 || len(script) > 4<<20 {
		return fmt.Errorf("SQL migration must contain 1 byte to 4 MiB")
	}
	masked, err := maskMigrationSQL(script)
	if err != nil {
		return err
	}
	count := 0
	for _, statement := range strings.Split(masked, ";") {
		words := strings.Fields(statement)
		if len(words) == 0 {
			continue
		}
		count++
		switch strings.ToUpper(words[0]) {
		case "CREATE", "ALTER", "DROP", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "COMMENT", "GRANT", "REVOKE", "DO", "WITH", "SELECT":
		default:
			return fmt.Errorf("unsupported migration statement %q; Scenery owns transaction and session control", words[0])
		}
	}
	if count == 0 {
		return fmt.Errorf("SQL migration contains no statement")
	}
	return nil
}

// ValidateInitialVerificationSQL requires one query, not a migration script.
// The app remains responsible for a read-only complete-schema predicate; SQL
// functions and data-modifying CTEs are not sandboxed by this shape check.
func ValidateInitialVerificationSQL(script string) error {
	if err := ValidateMigrationSQL(script); err != nil {
		return err
	}
	masked, err := maskMigrationSQL(script)
	if err != nil {
		return err
	}
	count := 0
	for _, statement := range strings.Split(masked, ";") {
		words := strings.Fields(statement)
		if len(words) == 0 {
			continue
		}
		count++
		if count != 1 || !strings.EqualFold(words[0], "SELECT") && !strings.EqualFold(words[0], "WITH") {
			return fmt.Errorf("initial verification must contain exactly one SELECT or WITH query")
		}
	}
	return nil
}

func maskMigrationSQL(script string) (string, error) {
	out := []byte(script)
	blank := func(start, end int) {
		for index := start; index < end; index++ {
			if out[index] != '\n' {
				out[index] = ' '
			}
		}
	}
	for index := 0; index < len(script); {
		start := index
		switch {
		case strings.HasPrefix(script[index:], "--"):
			for index < len(script) && script[index] != '\n' {
				index++
			}
			blank(start, index)
		case strings.HasPrefix(script[index:], "/*"):
			index += 2
			depth := 1
			for index < len(script) && depth > 0 {
				switch {
				case strings.HasPrefix(script[index:], "/*"):
					depth++
					index += 2
				case strings.HasPrefix(script[index:], "*/"):
					depth--
					index += 2
				default:
					index++
				}
			}
			if depth != 0 {
				return "", fmt.Errorf("unterminated SQL comment")
			}
			blank(start, index)
		case script[index] == '\'' || script[index] == '"':
			quote := script[index]
			escaped := quote == '\'' && index > 0 && (script[index-1] == 'E' || script[index-1] == 'e')
			index++
			closed := false
			for index < len(script) {
				if escaped && script[index] == '\\' {
					index += 2
					continue
				}
				if script[index] == quote {
					index++
					if index < len(script) && script[index] == quote {
						index++
						continue
					}
					closed = true
					break
				}
				index++
			}
			if !closed {
				return "", fmt.Errorf("unterminated SQL literal or identifier")
			}
			blank(start, index)
		case script[index] == '$':
			end := index + 1
			for end < len(script) && (script[end] == '_' || script[end] >= 'a' && script[end] <= 'z' || script[end] >= 'A' && script[end] <= 'Z' || end > index+1 && script[end] >= '0' && script[end] <= '9') {
				end++
			}
			if end >= len(script) || script[end] != '$' {
				index++
				continue
			}
			tag := script[index : end+1]
			closeAt := strings.Index(script[end+1:], tag)
			if closeAt < 0 {
				return "", fmt.Errorf("unterminated SQL dollar-quoted body")
			}
			index = end + 1 + closeAt + len(tag)
			blank(start, index)
		default:
			index++
		}
	}
	return string(out), nil
}
