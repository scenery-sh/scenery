package nativebuilddriver

import (
	"fmt"
	"strings"
)

func splitQuoted(input string) ([]string, error) {
	var result []string
	var current strings.Builder
	quote, escaped := rune(0), false
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated quoted flags")
	}
	flush()
	return result, nil
}
