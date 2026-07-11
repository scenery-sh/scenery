package runtime

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

type authorizationTokenKind uint8

const (
	authorizationEOF authorizationTokenKind = iota
	authorizationIdentifier
	authorizationString
	authorizationNumber
	authorizationLParen
	authorizationRParen
	authorizationLBracket
	authorizationRBracket
	authorizationComma
	authorizationNot
	authorizationEqual
	authorizationNotEqual
	authorizationLess
	authorizationLessEqual
	authorizationGreater
	authorizationGreaterEqual
	authorizationAnd
	authorizationOr
)

type authorizationToken struct {
	kind authorizationTokenKind
	text string
}

type authorizationLexer struct {
	source string
	offset int
}

func (lexer *authorizationLexer) next() (authorizationToken, error) {
	for lexer.offset < len(lexer.source) && unicode.IsSpace(rune(lexer.source[lexer.offset])) {
		lexer.offset++
	}
	if lexer.offset == len(lexer.source) {
		return authorizationToken{kind: authorizationEOF}, nil
	}
	start := lexer.offset
	character := lexer.source[lexer.offset]
	if character == '"' {
		lexer.offset++
		escaped := false
		for lexer.offset < len(lexer.source) {
			current := lexer.source[lexer.offset]
			lexer.offset++
			if current == '"' && !escaped {
				text := lexer.source[start:lexer.offset]
				if _, err := strconv.Unquote(text); err != nil {
					return authorizationToken{}, fmt.Errorf("invalid authorization string: %w", err)
				}
				return authorizationToken{kind: authorizationString, text: text}, nil
			}
			if current == '\\' && !escaped {
				escaped = true
			} else {
				escaped = false
			}
		}
		return authorizationToken{}, fmt.Errorf("unterminated authorization string")
	}
	if isAuthorizationIdentifierStart(character) {
		lexer.offset++
		for lexer.offset < len(lexer.source) && isAuthorizationIdentifierContinue(lexer.source[lexer.offset]) {
			lexer.offset++
		}
		return authorizationToken{kind: authorizationIdentifier, text: lexer.source[start:lexer.offset]}, nil
	}
	if character >= '0' && character <= '9' || character == '-' && lexer.offset+1 < len(lexer.source) && lexer.source[lexer.offset+1] >= '0' && lexer.source[lexer.offset+1] <= '9' {
		lexer.offset++
		for lexer.offset < len(lexer.source) {
			current := lexer.source[lexer.offset]
			if current < '0' || current > '9' {
				if current != '.' && current != 'e' && current != 'E' && current != '+' && current != '-' {
					break
				}
			}
			lexer.offset++
		}
		text := lexer.source[start:lexer.offset]
		if _, ok := new(big.Rat).SetString(text); !ok {
			return authorizationToken{}, fmt.Errorf("invalid authorization number %q", text)
		}
		return authorizationToken{kind: authorizationNumber, text: text}, nil
	}
	lexer.offset++
	token := authorizationToken{text: lexer.source[start:lexer.offset]}
	switch character {
	case '(':
		token.kind = authorizationLParen
	case ')':
		token.kind = authorizationRParen
	case '[':
		token.kind = authorizationLBracket
	case ']':
		token.kind = authorizationRBracket
	case ',':
		token.kind = authorizationComma
	case '!':
		if lexer.consume('=') {
			token.kind, token.text = authorizationNotEqual, "!="
		} else {
			token.kind = authorizationNot
		}
	case '=':
		if !lexer.consume('=') {
			return authorizationToken{}, fmt.Errorf("authorization expressions require ==")
		}
		token.kind, token.text = authorizationEqual, "=="
	case '<':
		if lexer.consume('=') {
			token.kind, token.text = authorizationLessEqual, "<="
		} else {
			token.kind = authorizationLess
		}
	case '>':
		if lexer.consume('=') {
			token.kind, token.text = authorizationGreaterEqual, ">="
		} else {
			token.kind = authorizationGreater
		}
	case '&':
		if !lexer.consume('&') {
			return authorizationToken{}, fmt.Errorf("authorization expressions require &&")
		}
		token.kind, token.text = authorizationAnd, "&&"
	case '|':
		if !lexer.consume('|') {
			return authorizationToken{}, fmt.Errorf("authorization expressions require ||")
		}
		token.kind, token.text = authorizationOr, "||"
	default:
		return authorizationToken{}, fmt.Errorf("unsupported authorization token %q", character)
	}
	return token, nil
}

func (lexer *authorizationLexer) consume(character byte) bool {
	if lexer.offset >= len(lexer.source) || lexer.source[lexer.offset] != character {
		return false
	}
	lexer.offset++
	return true
}

func isAuthorizationIdentifierStart(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_'
}

func isAuthorizationIdentifierContinue(character byte) bool {
	return isAuthorizationIdentifierStart(character) || character >= '0' && character <= '9' || character == '.'
}

type authorizationUnknown struct{}

type authorizationParser struct {
	lexer        authorizationLexer
	current      authorizationToken
	variables    map[string]any
	validateOnly bool
}

func evaluateContractAuthorizationRule(source string, variables map[string]any) (bool, error) {
	value, err := parseContractAuthorizationExpression(source, variables, false)
	if err != nil {
		return false, err
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("authorization expression did not produce bool")
	}
	return result, nil
}

func ValidateContractAuthorizationExpression(source string) error {
	value, err := parseContractAuthorizationExpression(source, nil, true)
	if err != nil {
		return err
	}
	if _, boolean := value.(bool); !boolean {
		if _, unknown := value.(authorizationUnknown); !unknown {
			return fmt.Errorf("authorization expression does not produce bool")
		}
	}
	return nil
}

// ValidateContractAuthorizationExpressionAgainst type-checks and evaluates an
// authorization expression against compiler-supplied representative values.
// Missing paths and incompatible operators are rejected before runtime.
func ValidateContractAuthorizationExpressionAgainst(source string, variables map[string]any) error {
	value, err := parseContractAuthorizationExpression(source, variables, false)
	if err != nil {
		return err
	}
	if _, ok := value.(bool); !ok {
		return fmt.Errorf("authorization expression does not produce bool")
	}
	return nil
}

func parseContractAuthorizationExpression(source string, variables map[string]any, validateOnly bool) (any, error) {
	parser := &authorizationParser{lexer: authorizationLexer{source: strings.TrimSpace(source)}, variables: variables, validateOnly: validateOnly}
	if parser.lexer.source == "" {
		return nil, fmt.Errorf("authorization expression is empty")
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	value, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.current.kind != authorizationEOF {
		return nil, fmt.Errorf("unexpected authorization token %q", parser.current.text)
	}
	return value, nil
}

func (parser *authorizationParser) advance() error {
	token, err := parser.lexer.next()
	if err != nil {
		return err
	}
	parser.current = token
	return nil
}

func (parser *authorizationParser) parseOr() (any, error) {
	left, err := parser.parseAnd()
	for err == nil && parser.current.kind == authorizationOr {
		_ = parser.advance()
		var right any
		right, err = parser.parseAnd()
		if err == nil {
			left, err = authorizationLogical(left, right, false)
		}
	}
	return left, err
}

func (parser *authorizationParser) parseAnd() (any, error) {
	left, err := parser.parseComparison()
	for err == nil && parser.current.kind == authorizationAnd {
		_ = parser.advance()
		var right any
		right, err = parser.parseComparison()
		if err == nil {
			left, err = authorizationLogical(left, right, true)
		}
	}
	return left, err
}

func (parser *authorizationParser) parseComparison() (any, error) {
	left, err := parser.parseUnary()
	if err != nil {
		return nil, err
	}
	operator := parser.current.kind
	if operator < authorizationEqual || operator > authorizationGreaterEqual {
		return left, nil
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	right, err := parser.parseUnary()
	if err != nil {
		return nil, err
	}
	return authorizationCompare(left, right, operator)
}

func (parser *authorizationParser) parseUnary() (any, error) {
	if parser.current.kind != authorizationNot {
		return parser.parsePrimary()
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	value, err := parser.parseUnary()
	if _, unknown := value.(authorizationUnknown); unknown {
		return value, err
	}
	boolean, ok := value.(bool)
	if err != nil || !ok {
		return nil, fmt.Errorf("authorization ! requires bool")
	}
	return !boolean, nil
}

func (parser *authorizationParser) parsePrimary() (any, error) {
	token := parser.current
	switch token.kind {
	case authorizationString:
		if err := parser.advance(); err != nil {
			return nil, err
		}
		return strconv.Unquote(token.text)
	case authorizationNumber:
		if err := parser.advance(); err != nil {
			return nil, err
		}
		value, _ := new(big.Rat).SetString(token.text)
		return value, nil
	case authorizationIdentifier:
		if err := parser.advance(); err != nil {
			return nil, err
		}
		switch token.text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		if parser.current.kind == authorizationLParen {
			return parser.parseCall(token.text)
		}
		if parser.validateOnly {
			return authorizationUnknown{}, nil
		}
		return resolveAuthorizationVariable(parser.variables, token.text)
	case authorizationLParen:
		if err := parser.advance(); err != nil {
			return nil, err
		}
		value, err := parser.parseOr()
		if err != nil || parser.current.kind != authorizationRParen {
			return nil, fmt.Errorf("authorization expression has unclosed parentheses")
		}
		return value, parser.advance()
	case authorizationLBracket:
		return parser.parseList()
	default:
		return nil, fmt.Errorf("expected authorization value, got %q", token.text)
	}
}

func (parser *authorizationParser) parseCall(name string) (any, error) {
	if name != "contains" {
		return nil, fmt.Errorf("unsupported authorization function %s", name)
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	collection, err := parser.parseOr()
	if err != nil || parser.current.kind != authorizationComma {
		return nil, fmt.Errorf("contains requires two arguments")
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	needle, err := parser.parseOr()
	if err != nil || parser.current.kind != authorizationRParen {
		return nil, fmt.Errorf("contains requires two arguments")
	}
	if err := parser.advance(); err != nil {
		return nil, err
	}
	if _, unknown := collection.(authorizationUnknown); unknown {
		return authorizationUnknown{}, nil
	}
	value := reflect.ValueOf(collection)
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, fmt.Errorf("contains requires a list")
	}
	for index := 0; index < value.Len(); index++ {
		equal, comparable := authorizationEqualValues(value.Index(index).Interface(), needle)
		if !comparable {
			return nil, fmt.Errorf("contains requires compatible element and needle types")
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}

func (parser *authorizationParser) parseList() (any, error) {
	if err := parser.advance(); err != nil {
		return nil, err
	}
	var values []any
	if parser.current.kind == authorizationRBracket {
		return values, parser.advance()
	}
	for {
		value, err := parser.parseOr()
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		if parser.current.kind == authorizationRBracket {
			return values, parser.advance()
		}
		if parser.current.kind != authorizationComma {
			return nil, fmt.Errorf("authorization list requires comma")
		}
		if err := parser.advance(); err != nil {
			return nil, err
		}
	}
}

func authorizationLogical(left, right any, and bool) (any, error) {
	if _, unknown := left.(authorizationUnknown); unknown {
		return authorizationUnknown{}, nil
	}
	if _, unknown := right.(authorizationUnknown); unknown {
		return authorizationUnknown{}, nil
	}
	a, aOK := left.(bool)
	b, bOK := right.(bool)
	if !aOK || !bOK {
		return nil, fmt.Errorf("authorization logical operator requires bool operands")
	}
	if and {
		return a && b, nil
	}
	return a || b, nil
}

func authorizationCompare(left, right any, operator authorizationTokenKind) (any, error) {
	if _, unknown := left.(authorizationUnknown); unknown {
		return authorizationUnknown{}, nil
	}
	if _, unknown := right.(authorizationUnknown); unknown {
		return authorizationUnknown{}, nil
	}
	equal, comparable := authorizationEqualValues(left, right)
	if operator == authorizationEqual {
		if !comparable {
			return nil, fmt.Errorf("authorization equality requires compatible operands")
		}
		return equal, nil
	}
	if operator == authorizationNotEqual {
		if !comparable {
			return nil, fmt.Errorf("authorization equality requires compatible operands")
		}
		return !equal, nil
	}
	if !comparable {
		return nil, fmt.Errorf("authorization values are not comparable")
	}
	comparison, err := authorizationOrder(left, right)
	if err != nil {
		return nil, err
	}
	switch operator {
	case authorizationLess:
		return comparison < 0, nil
	case authorizationLessEqual:
		return comparison <= 0, nil
	case authorizationGreater:
		return comparison > 0, nil
	case authorizationGreaterEqual:
		return comparison >= 0, nil
	default:
		return nil, fmt.Errorf("unknown authorization comparison")
	}
}

func authorizationEqualValues(left, right any) (bool, bool) {
	if leftNumber, ok := authorizationNumberValue(left); ok {
		if rightNumber, ok := authorizationNumberValue(right); ok {
			return leftNumber.Cmp(rightNumber) == 0, true
		}
	}
	if left == nil || right == nil {
		return left == nil && right == nil, true
	}
	leftType, rightType := reflect.TypeOf(left), reflect.TypeOf(right)
	if leftType != rightType {
		return false, false
	}
	return reflect.DeepEqual(left, right), true
}

func authorizationOrder(left, right any) (int, error) {
	if leftNumber, ok := authorizationNumberValue(left); ok {
		if rightNumber, ok := authorizationNumberValue(right); ok {
			return leftNumber.Cmp(rightNumber), nil
		}
	}
	leftString, leftOK := left.(string)
	rightString, rightOK := right.(string)
	if leftOK && rightOK {
		return strings.Compare(leftString, rightString), nil
	}
	return 0, fmt.Errorf("authorization ordering requires two numbers or strings")
}

func authorizationNumberValue(value any) (*big.Rat, bool) {
	switch typed := value.(type) {
	case *big.Rat:
		return typed, true
	case json.Number:
		result, ok := new(big.Rat).SetString(typed.String())
		return result, ok
	case int:
		return new(big.Rat).SetInt64(int64(typed)), true
	case int64:
		return new(big.Rat).SetInt64(typed), true
	case uint64:
		return new(big.Rat).SetUint64(typed), true
	case float64:
		result := new(big.Rat)
		return result.SetFloat64(typed), true
	default:
		return nil, false
	}
}

func resolveAuthorizationVariable(variables map[string]any, path string) (any, error) {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("authorization variable %s requires an allowed root and field", path)
	}
	current, ok := variables[parts[0]]
	if !ok {
		return nil, fmt.Errorf("authorization root %s is unavailable", parts[0])
	}
	for _, part := range parts[1:] {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("authorization path %s is not an object", path)
		}
		current, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("authorization path %s is unavailable", path)
		}
	}
	return current, nil
}
