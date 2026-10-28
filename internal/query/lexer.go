// Package query разбирает и типизирует минимальный язык запросов платформы.
//
// Язык — небольшое собственное подмножество в духе запросов 1С, не её полная грамматика:
//
//	ВЫБРАТЬ Товар, Количество КАК Остаток
//	ИЗ РегистрНакопления.Запасы.Остатки
//	ГДЕ Товар = &Товар И Количество > 0
//	УПОРЯДОЧИТЬ ПО Количество УБЫВ
//
// Ключевые слова есть и в английской форме (SELECT, FROM, WHERE, AND, OR, NOT, ORDER BY, AS, ASC, DESC,
// TRUE, FALSE; AccumulationRegister.<имя>.Balance). Запрос разбирается вручную, без генератора и без
// заимствованной грамматики, и сразу превращается в типизированное промежуточное представление (Query):
// имена полей и регистра проверены по метаданным, у каждого параметра выведен тип. SQL из текста запроса не
// строится и не исполняется: исполнение (следующая часть P17) идёт над API ядра, числа — точным value.Number.
// Всё, чего в подмножестве нет (соединения, группировка, функции, IN, LIKE и т. д.), даёт явную ошибку
// Unsupported с позицией, а не молчаливый пропуск. Поддерживаемый список — docs/implementation/query.md.
package query

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/unxed/oddyness/internal/value"
)

// Pos — позиция в тексте запроса: строка и колонка (в символах), с единицы.
type Pos struct{ Line, Col int }

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Error — диагностика с позицией. Unsupported отличает осознанно не поддерживаемую конструкцию
// от синтаксической или смысловой ошибки.
type Error struct {
	Pos         Pos
	Msg         string
	Unsupported bool
}

func (e *Error) Error() string { return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Col, e.Msg) }

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString
	tParam // &Имя
	tOp
)

type token struct {
	kind tokKind
	text string // идентификатор, оператор, значение строки или имя параметра
	pos  Pos
	num  value.Number
}

type lexer struct {
	src       []rune
	i         int
	line, col int
}

func newLexer(src string) *lexer { return &lexer{src: []rune(src), line: 1, col: 1} }

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

func fail(pos Pos, unsupported bool, format string, args ...any) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...), Unsupported: unsupported}
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

func (l *lexer) scan() (token, *Error) {
	l.skipSpace()
	pos := l.pos()
	if l.i >= len(l.src) {
		return token{kind: tEOF, pos: pos}, nil
	}
	r := l.peek(0)
	switch {
	case isIdentStart(r):
		return token{kind: tIdent, text: l.ident(), pos: pos}, nil
	case isDigit(r):
		return l.number(pos)
	case r == '"':
		return l.str(pos)
	case r == '&':
		l.advance()
		if !isIdentStart(l.peek(0)) {
			return token{}, fail(pos, false, "parameter name expected after '&'")
		}
		return token{kind: tParam, text: l.ident(), pos: pos}, nil
	case r == '\'':
		return token{}, fail(pos, true, "date literals are not supported; pass a date as a parameter")
	}
	l.advance()
	switch r {
	case '<':
		if n := l.peek(0); n == '=' || n == '>' {
			l.advance()
			return token{kind: tOp, text: "<" + string(n), pos: pos}, nil
		}
		return token{kind: tOp, text: "<", pos: pos}, nil
	case '>':
		if l.peek(0) == '=' {
			l.advance()
			return token{kind: tOp, text: ">=", pos: pos}, nil
		}
		return token{kind: tOp, text: ">", pos: pos}, nil
	case '=', '(', ')', ',', '.', '-', '*', '+', '/', '%', '[', ']', '?':
		return token{kind: tOp, text: string(r), pos: pos}, nil
	}
	return token{}, fail(pos, false, "unexpected character %q", r)
}

func (l *lexer) ident() string {
	start := l.i
	for l.i < len(l.src) && isIdentPart(l.peek(0)) {
		l.advance()
	}
	return string(l.src[start:l.i])
}

func (l *lexer) number(pos Pos) (token, *Error) {
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
		return token{}, fail(pos, false, "invalid number literal %q: unexpected %q", text+string(r), r)
	}
	n, err := value.Parse(text)
	if err != nil {
		return token{}, fail(pos, false, "number literal %s: %v", text, err)
	}
	return token{kind: tNumber, text: text, pos: pos, num: n}, nil
}

// str читает "..." с удвоенной кавычкой; перенос строки внутри литерала — ошибка (литерал одной строки).
func (l *lexer) str(pos Pos) (token, *Error) {
	l.advance()
	var sb strings.Builder
	for {
		if l.i >= len(l.src) || l.peek(0) == '\n' {
			return token{}, fail(pos, false, "unterminated string literal")
		}
		r := l.advance()
		if r == '"' {
			if l.peek(0) == '"' {
				l.advance()
				sb.WriteRune('"')
				continue
			}
			return token{kind: tString, text: sb.String(), pos: pos}, nil
		}
		sb.WriteRune(r)
	}
}
