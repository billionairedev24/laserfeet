package lang

import (
	"fmt"
	"strings"
)

// TokenType says what kind of piece of source text a token is.
type TokenType int

const (
	EOF TokenType = iota
	NEWLINE
	IDENT
	NUMBER
	STRING
	KEYWORD
	SYMBOL
)

// Token is one piece of Laser Feet source code.
type Token struct {
	Type TokenType
	Text string
	Line int
}

var keywords = map[string]bool{
	"launch": true, "show": true, "beam": true, "send": true, "lock": true,
	"object": true, "rule": true, "follows": true, "when": true, "otherwise": true,
	"each": true, "in": true, "loop": true, "while": true, "times": true,
	"then": true, "fast": true, "attempt": true, "fails": true, "fail": true,
	"use": true, "test": true, "expect": true, "is": true, "not": true,
	"and": true, "or": true, "yes": true, "no": true, "nothing": true,
	"me": true, "stop": true, "skip": true, "to": true,
}

// Placeholders for escaped braces inside text, so they are not treated as {interpolation}.
const (
	litOpen  = '\x01'
	litClose = '\x02'
)

// SyntaxError is a problem found while reading the code.
type SyntaxError struct {
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("Problem on line %d: %s", e.Line, e.Msg) }

// Lex turns source text into tokens.
func Lex(src string, startLine int) ([]Token, error) {
	var toks []Token
	line := startLine
	depth := 0 // inside ( ) or [ ], newlines are ignored
	rs := []rune(src)
	i := 0
	emit := func(t TokenType, text string) { toks = append(toks, Token{t, text, line}) }
	for i < len(rs) {
		c := rs[i]
		switch {
		case c == '\n':
			if depth == 0 && len(toks) > 0 && toks[len(toks)-1].Type != NEWLINE {
				emit(NEWLINE, "\\n")
			}
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '-' && i+1 < len(rs) && rs[i+1] == '-':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case c >= '0' && c <= '9':
			start := i
			for i < len(rs) && (rs[i] >= '0' && rs[i] <= '9' || rs[i] == '_') {
				i++
			}
			if i+1 < len(rs) && rs[i] == '.' && rs[i+1] >= '0' && rs[i+1] <= '9' {
				i++
				for i < len(rs) && rs[i] >= '0' && rs[i] <= '9' {
					i++
				}
			}
			emit(NUMBER, strings.ReplaceAll(string(rs[start:i]), "_", ""))
		case isLetter(c):
			start := i
			for i < len(rs) && (isLetter(rs[i]) || rs[i] >= '0' && rs[i] <= '9') {
				i++
			}
			word := string(rs[start:i])
			if keywords[word] {
				emit(KEYWORD, word)
			} else {
				emit(IDENT, word)
			}
		case c == '"':
			startLine := line
			i++
			var sb strings.Builder
			closed := false
			for i < len(rs) {
				ch := rs[i]
				if ch == '"' {
					closed = true
					i++
					break
				}
				if ch == '\n' {
					return nil, &SyntaxError{startLine, "this text is missing its closing quote \""}
				}
				if ch == '\\' && i+1 < len(rs) {
					i++
					switch rs[i] {
					case 'n':
						sb.WriteRune('\n')
					case 't':
						sb.WriteRune('\t')
					case '{':
						sb.WriteRune(litOpen)
					case '}':
						sb.WriteRune(litClose)
					default:
						sb.WriteRune(rs[i])
					}
					i++
					continue
				}
				sb.WriteRune(ch)
				i++
			}
			if !closed {
				return nil, &SyntaxError{startLine, "this text is missing its closing quote \""}
			}
			toks = append(toks, Token{STRING, sb.String(), startLine})
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "<=", ">=", "==", "!=", "=>":
				emit(SYMBOL, two)
				i += 2
				continue
			}
			switch c {
			case '(', '[':
				depth++
			case ')', ']':
				if depth > 0 {
					depth--
				}
			}
			if strings.ContainsRune("{}()[],.:=+-*/%<>", c) {
				emit(SYMBOL, string(c))
				i++
				continue
			}
			return nil, &SyntaxError{line, fmt.Sprintf("I don't understand the character '%c'", c)}
		}
	}
	emit(NEWLINE, "\\n")
	emit(EOF, "end of file")
	return toks, nil
}

func isLetter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}
