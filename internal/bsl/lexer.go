// Package bsl разбирает поддерживаемое подмножество встроенного языка (BSL).
//
// Парсер написан вручную: числовые литералы сразу становятся точными
// value.Number (без float64), неподдерживаемые конструкции дают явную ошибку
// с позицией и не пропускаются молча. Поддерживаемый список конструкций —
// в docs/implementation/bsl-subset.md.
package bsl

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/unxed/oddyness/internal/value"
)

// Pos — позиция в тексте: строка и колонка (в символах), с единицы.
type Pos struct{ Line, Col int }

// Position возвращает саму позицию; встраивание Pos даёт этот метод узлам AST.
func (p Pos) Position() Pos { return p }

// Error — диагностика с позицией. Unsupported отличает осознанно не
// поддерживаемую конструкцию от синтаксической ошибки.
type Error struct {
	Pos         Pos
	Msg         string
	Unsupported bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Col, e.Msg)
}

type kind int

const (
	kEOF kind = iota
	kIdent
	kNumber
	kString
	kOp
	kDirective // &НаСервере
	kPreproc   // #Если ...
	kErr
)

type token struct {
	kind kind
	text string
	pos  Pos
	num  value.Number // kNumber
	err  *Error       // kErr
}

type lexer struct {
	src       []rune
	i         int
	line, col int
}

func newLexer(src string) *lexer {
	return &lexer{src: []rune(src), line: 1, col: 1}
}

func (l *lexer) peek(off int) rune {
	if l.i+off < len(l.src) {
		return l.src[l.i+off]
	}
	return 0
}

func (l *lexer) advance() rune {
	r := l.src[l.i]
	l.i++
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *lexer) pos() Pos { return Pos{l.line, l.col} }

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return isIdentStart(r) || unicode.IsDigit(r) }
func isDigit(r rune) bool      { return r >= '0' && r <= '9' }

func errTok(pos Pos, unsupported bool, format string, args ...any) token {
	return token{kind: kErr, pos: pos, err: &Error{Pos: pos, Msg: fmt.Sprintf(format, args...), Unsupported: unsupported}}
}

func (l *lexer) skipSpace() {
	for l.i < len(l.src) {
		r := l.peek(0)
		switch {
		case unicode.IsSpace(r):
			l.advance()
		case r == '/' && l.peek(1) == '/':
			for l.i < len(l.src) && l.peek(0) != '\n' {
				l.advance()
			}
		default:
			return
		}
	}
}

func (l *lexer) scan() token {
	l.skipSpace()
	pos := l.pos()
	if l.i >= len(l.src) {
		return token{kind: kEOF, pos: pos}
	}
	r := l.peek(0)
	switch {
	case isIdentStart(r):
		start := l.i
		for l.i < len(l.src) && isIdentPart(l.peek(0)) {
			l.advance()
		}
		return token{kind: kIdent, text: string(l.src[start:l.i]), pos: pos}
	case isDigit(r):
		return l.scanNumber(pos)
	case r == '"':
		return l.scanString(pos)
	case r == '\'':
		return errTok(pos, true, "date literals are not supported")
	case r == '&':
		l.advance()
		start := l.i
		for l.i < len(l.src) && isIdentPart(l.peek(0)) {
			l.advance()
		}
		return token{kind: kDirective, text: "&" + string(l.src[start:l.i]), pos: pos}
	case r == '#':
		start := l.i
		for l.i < len(l.src) && l.peek(0) != '\n' {
			l.advance()
		}
		return token{kind: kPreproc, text: strings.TrimSpace(string(l.src[start:l.i])), pos: pos}
	}
	l.advance()
	switch r {
	case '<':
		if n := l.peek(0); n == '=' || n == '>' {
			l.advance()
			return token{kind: kOp, text: "<" + string(n), pos: pos}
		}
		return token{kind: kOp, text: "<", pos: pos}
	case '>':
		if l.peek(0) == '=' {
			l.advance()
			return token{kind: kOp, text: ">=", pos: pos}
		}
		return token{kind: kOp, text: ">", pos: pos}
	case '(', ')', ',', ';', '=', '+', '-', '*', '/', '%', '.', '[', ']', '?':
		return token{kind: kOp, text: string(r), pos: pos}
	}
	return errTok(pos, false, "unexpected character %q", r)
}

func (l *lexer) scanNumber(pos Pos) token {
	start := l.i
	for isDigit(l.peek(0)) {
		l.advance()
	}
	if l.peek(0) == '.' && isDigit(l.peek(1)) {
		l.advance()
		for isDigit(l.peek(0)) {
			l.advance()
		}
	}
	text := string(l.src[start:l.i])
	if r := l.peek(0); isIdentStart(r) {
		return errTok(pos, false, "invalid number literal %q: unexpected %q", text+string(r), r)
	}
	n, err := value.Parse(text)
	if err != nil {
		return errTok(pos, false, "number literal %s: %v", text, err)
	}
	return token{kind: kNumber, text: text, pos: pos, num: n}
}

// scanString читает "..." с удвоенной кавычкой; перенос строки внутри строки
// допустим только перед продолжением с символом '|' в начале следующей строки.
func (l *lexer) scanString(pos Pos) token {
	l.advance() // открывающая кавычка
	var sb strings.Builder
	for {
		if l.i >= len(l.src) {
			return errTok(pos, false, "unterminated string literal")
		}
		r := l.advance()
		switch {
		case r == '"':
			if l.peek(0) == '"' {
				l.advance()
				sb.WriteRune('"')
				continue
			}
			return token{kind: kString, text: sb.String(), pos: pos}
		case r == '\r':
			// часть \r\n: сам перенос обработает следующий шаг
		case r == '\n':
			for l.peek(0) == ' ' || l.peek(0) == '\t' || l.peek(0) == '\r' {
				l.advance()
			}
			if l.peek(0) != '|' {
				return errTok(pos, false, "unterminated string literal: a continuation line must start with '|'")
			}
			l.advance()
			sb.WriteRune('\n')
		default:
			sb.WriteRune(r)
		}
	}
}
