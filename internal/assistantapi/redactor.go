package assistantapi

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const redactionReplacement = "assistant runtime"

type redactionPattern struct {
	literal        string
	token          bool
	boundaryBefore bool
}

// Redactor normalizes and redacts public text incrementally. It retains a
// bounded suffix so a sensitive lexical term split across writes is handled
// exactly like the same term in one write.
type Redactor struct {
	pending  string
	patterns []redactionPattern
	maxTail  int
}

func NewRedactor() *Redactor {
	return NewRedactorWithPatterns(defaultRedactionPatterns())
}

// NewRedactorWithPatterns is useful for conformance tests and for extending
// the current signature catalogue without coupling this package to a helper.
// Plain identifier patterns are matched as whole lexical tokens; patterns
// containing punctuation are matched as exact signatures.
func NewRedactorWithPatterns(patterns []string) *Redactor {
	compiled := make([]redactionPattern, 0, len(patterns))
	maxTail := 1
	for _, pattern := range patterns {
		pattern = norm.NFKC.String(strings.ToValidUTF8(pattern, ""))
		if pattern == "" {
			continue
		}
		token := true
		for _, r := range pattern {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
				token = false
				break
			}
		}
		boundaryBefore := strings.HasSuffix(pattern, "_") || strings.HasSuffix(pattern, "-")
		compiled = append(compiled, redactionPattern{literal: pattern, token: token, boundaryBefore: boundaryBefore})
		if count := utf8.RuneCountInString(pattern) + 2; count > maxTail {
			maxTail = count
		}
	}
	sort.SliceStable(compiled, func(i, j int) bool {
		return len(compiled[i].literal) > len(compiled[j].literal)
	})
	return &Redactor{patterns: compiled, maxTail: maxTail}
}

// Write accepts one UTF-8 chunk and returns the safe redacted prefix. Call
// Flush after the final chunk to emit the retained suffix.
func (r *Redactor) Write(chunk string) string {
	if r == nil || chunk == "" {
		return ""
	}
	r.pending = norm.NFKC.String(strings.ToValidUTF8(r.pending+chunk, "�"))
	limit := r.safeLimit()
	output, consumed := redactPrefix(r.pending, limit, r.patterns)
	if consumed > 0 {
		r.pending = r.pending[consumed:]
	}
	return output
}

func (r *Redactor) Flush() string {
	if r == nil || r.pending == "" {
		return ""
	}
	r.pending = norm.NFKC.String(strings.ToValidUTF8(r.pending, "�"))
	output, consumed := redactPrefix(r.pending, len(r.pending), r.patterns)
	r.pending = r.pending[consumed:]
	return output
}

func (r *Redactor) Reset() {
	if r != nil {
		r.pending = ""
	}
}

func RedactString(value string) string {
	redactor := NewRedactor()
	return redactor.Write(value) + redactor.Flush()
}

func RedactChunks(chunks []string) string {
	redactor := NewRedactor()
	var builder strings.Builder
	for _, chunk := range chunks {
		builder.WriteString(redactor.Write(chunk))
	}
	builder.WriteString(redactor.Flush())
	return builder.String()
}

func (r *Redactor) safeLimit() int {
	if r.maxTail <= 0 {
		r.maxTail = 1
	}
	runes := []rune(r.pending)
	if len(runes) <= r.maxTail {
		return 0
	}
	return len(string(runes[:len(runes)-r.maxTail]))
}

func redactPrefix(input string, limit int, patterns []redactionPattern) (string, int) {
	if limit <= 0 || input == "" {
		return "", 0
	}
	var output strings.Builder
	position := 0
	for position < len(input) && position < limit {
		pattern, ok := matchingPattern(input, position, patterns)
		if ok {
			end := position + len(pattern.literal)
			if end > limit {
				break
			}
			output.WriteString(redactionReplacement)
			position = end
			continue
		}
		_, size := utf8.DecodeRuneInString(input[position:])
		if size == 0 {
			break
		}
		output.WriteString(input[position : position+size])
		position += size
	}
	return output.String(), position
}

func matchingPattern(input string, position int, patterns []redactionPattern) (redactionPattern, bool) {
	for _, pattern := range patterns {
		end := position + len(pattern.literal)
		if end > len(input) || !strings.EqualFold(input[position:end], pattern.literal) {
			continue
		}
		if pattern.token && !tokenBoundary(input, position, end) {
			continue
		}
		if pattern.boundaryBefore && position > 0 {
			previous, _ := utf8.DecodeLastRuneInString(input[:position])
			if isWordRune(previous) {
				continue
			}
		}
		return pattern, true
	}
	return redactionPattern{}, false
}

func tokenBoundary(input string, start, end int) bool {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(input[:start])
		if isWordRune(previous) {
			return false
		}
	}
	if end < len(input) {
		next, _ := utf8.DecodeRuneInString(input[end:])
		if isWordRune(next) {
			return false
		}
	}
	return true
}

func isWordRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || unicode.IsMark(value) || value == '_'
}

func defaultRedactionPatterns() []string {
	// Keep the sensitive token assembled so public source and test fixtures do
	// not accidentally publish a provider identifier.
	token := string([]rune{'e', 'v', 'e'})
	return []string{
		token,
		"/" + token + "/",
		"/" + token + "/v1",
		token + "_",
		token + "-",
		"node_modules/" + token,
		"from \"" + token + "\"",
		"from \"" + token + "/",
		"@" + "ver" + "cel/connect/" + token,
	}
}
