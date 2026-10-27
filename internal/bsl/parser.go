package bsl

import (
	"fmt"
	"strings"
)

// Ключевые слова поддерживаемого подмножества: русская и английская формы,
// регистр не важен. Значение — каноническое английское имя.
var keywords = map[string]string{
	"процедура": "procedure", "procedure": "procedure",
	"конецпроцедуры": "endprocedure", "endprocedure": "endprocedure",
	"функция": "function", "function": "function",
	"конецфункции": "endfunction", "endfunction": "endfunction",
	"перем": "var", "var": "var",
	"возврат": "return", "return": "return",
	"знач": "val", "val": "val",
	"экспорт": "export", "export": "export",
	"истина": "true", "true": "true",
	"ложь": "false", "false": "false",
	"неопределено": "undefined", "undefined": "undefined",
	"null": "null",
	"и":    "and", "and": "and",
	"или": "or", "or": "or",
	"не": "not", "not": "not",
}

// Зарезервированные слова языка 1С, которых в подмножестве нет: встретив их,
// парсер отвечает явной ошибкой «не поддерживается», а не «неожиданный токен».
var unsupportedWords = map[string]string{
	"если": "if", "if": "if", "иначеесли": "elsif", "elsif": "elsif",
	"иначе": "else", "else": "else", "конецесли": "endif", "endif": "endif",
	"тогда": "then", "then": "then",
	"пока": "while", "while": "while",
	"для": "for", "for": "for", "каждого": "each", "each": "each",
	"из": "in", "in": "in", "по": "to", "to": "to",
	"цикл": "do", "do": "do", "конеццикла": "enddo", "enddo": "enddo",
	"прервать": "break", "break": "break", "продолжить": "continue", "continue": "continue",
	"попытка": "try", "try": "try", "исключение": "except", "except": "except",
	"конецпопытки": "endtry", "endtry": "endtry",
	"вызватьисключение": "raise", "raise": "raise",
	"новый": "new", "new": "new",
	"перейти": "goto", "goto": "goto",
	"выполнить": "execute", "execute": "execute",
}

type parser struct {
	lx  *lexer
	tok token
}

// bail переносит ошибку из глубины разбора к Parse.
type bail struct{ err *Error }

// Parse разбирает модуль и возвращает первую по позиции ошибку (*Error).
func Parse(src string) (m *Module, err error) {
	defer func() {
		if r := recover(); r != nil {
			b, ok := r.(bail)
			if !ok {
				panic(r)
			}
			m, err = nil, b.err
		}
	}()
	p := &parser{lx: newLexer(src)}
	p.step()
	return p.module(), nil
}

// step читает следующий токен; ошибка лексера прерывает разбор.
func (p *parser) step() {
	p.tok = p.lx.scan()
	if p.tok.kind == kErr {
		panic(bail{p.tok.err})
	}
}

func (p *parser) fail(pos Pos, format string, args ...any) {
	panic(bail{&Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}})
}

func (p *parser) unsupported(pos Pos, format string, args ...any) {
	panic(bail{&Error{Pos: pos, Msg: fmt.Sprintf(format, args...) + " is not supported", Unsupported: true}})
}

func (p *parser) take() token {
	t := p.tok
	p.step()
	return t
}

// kw возвращает каноническое имя ключевого слова токена или "".
func kw(t token) string {
	if t.kind != kIdent {
		return ""
	}
	return keywords[strings.ToLower(t.text)]
}

func unsupportedWord(t token) string {
	if t.kind != kIdent {
		return ""
	}
	return unsupportedWords[strings.ToLower(t.text)]
}

func (t token) describe() string {
	switch t.kind {
	case kEOF:
		return "end of file"
	case kString:
		return "string literal"
	case kNumber:
		return "number " + t.text
	}
	return fmt.Sprintf("%q", t.text)
}

func (p *parser) isOp(s string) bool { return p.tok.kind == kOp && p.tok.text == s }

func (p *parser) expectOp(s string) token {
	if !p.isOp(s) {
		p.fail(p.tok.pos, "expected %q, found %s", s, p.tok.describe())
	}
	return p.take()
}

// rejectDirective отклоняет директивы компиляции и препроцессор.
func (p *parser) rejectDirective() {
	switch p.tok.kind {
	case kDirective:
		p.unsupported(p.tok.pos, "compilation directive %s", p.tok.text)
	case kPreproc:
		p.unsupported(p.tok.pos, "preprocessor instruction %q", p.tok.text)
	}
}

// name читает имя (переменной, подпрограммы, параметра): не ключевое слово.
func (p *parser) name(what string) token {
	t := p.tok
	if t.kind != kIdent {
		p.fail(t.pos, "expected %s name, found %s", what, t.describe())
	}
	if k := kw(t); k != "" {
		p.fail(t.pos, "%q is a reserved word and cannot be used as %s name", t.text, what)
	}
	if unsupportedWord(t) != "" {
		p.fail(t.pos, "%q is a reserved word and cannot be used as %s name", t.text, what)
	}
	p.step()
	return t
}

func (p *parser) module() *Module {
	m := &Module{}
	seen := map[string]Pos{}
	for {
		p.rejectDirective()
		switch {
		case p.tok.kind == kEOF:
			return m
		case kw(p.tok) == "var":
			m.Vars = append(m.Vars, p.varDecl(true))
		case kw(p.tok) == "procedure" || kw(p.tok) == "function":
			r := p.routine()
			key := strings.ToLower(r.Name)
			if prev, dup := seen[key]; dup {
				p.fail(r.Pos, "%q is already declared at %d:%d", r.Name, prev.Line, prev.Col)
			}
			seen[key] = r.Pos
			m.Routines = append(m.Routines, r)
		case unsupportedWord(p.tok) != "":
			p.unsupported(p.tok.pos, "%q outside a procedure or function", p.tok.text)
		case p.tok.kind == kIdent:
			p.unsupported(p.tok.pos, "statement outside a procedure or function (module body)")
		default:
			p.fail(p.tok.pos, "expected procedure, function or variable declaration, found %s", p.tok.describe())
		}
	}
}

func (p *parser) varDecl(allowExport bool) *VarDecl {
	d := &VarDecl{Pos: p.take().pos}
	seen := map[string]bool{}
	for {
		n := p.name("variable")
		if seen[strings.ToLower(n.text)] {
			p.fail(n.pos, "variable %q is declared twice in one declaration", n.text)
		}
		seen[strings.ToLower(n.text)] = true
		d.Names = append(d.Names, n.text)
		if !p.isOp(",") {
			break
		}
		p.step()
	}
	if kw(p.tok) == "export" {
		if !allowExport {
			p.fail(p.tok.pos, "a local variable cannot be exported")
		}
		d.Export = true
		p.step()
	}
	p.expectOp(";")
	return d
}

func (p *parser) routine() *Routine {
	head := p.take()
	r := &Routine{Pos: head.pos, IsFunction: kw(head) == "function"}
	r.Name = p.name("routine").text
	p.expectOp("(")
	seen := map[string]bool{}
	if !p.isOp(")") {
		for {
			par := Param{Pos: p.tok.pos}
			if kw(p.tok) == "val" {
				par.ByVal = true
				p.step()
			}
			n := p.name("parameter")
			if seen[strings.ToLower(n.text)] {
				p.fail(n.pos, "parameter %q is declared twice", n.text)
			}
			seen[strings.ToLower(n.text)] = true
			par.Name = n.text
			if p.isOp("=") {
				p.unsupported(p.tok.pos, "default parameter value")
			}
			r.Params = append(r.Params, par)
			if !p.isOp(",") {
				break
			}
			p.step()
		}
	}
	p.expectOp(")")
	if kw(p.tok) == "export" {
		r.Export = true
		p.step()
	}
	end := "endprocedure"
	if r.IsFunction {
		end = "endfunction"
	}
	for kw(p.tok) == "var" {
		r.Locals = append(r.Locals, p.varDecl(false))
	}
	for {
		p.rejectDirective()
		k := kw(p.tok)
		switch {
		case k == end:
			p.step()
			return r
		case k == "endprocedure" || k == "endfunction":
			p.fail(p.tok.pos, "%q does not close %s %q", p.tok.text, routineWord(r), r.Name)
		case p.tok.kind == kEOF:
			p.fail(p.tok.pos, "unexpected end of file: %s %q is not closed", routineWord(r), r.Name)
		case p.isOp(";"):
			p.step()
			continue
		case k == "var":
			p.fail(p.tok.pos, "variable declarations must precede statements")
		}
		r.Body = append(r.Body, p.statement(r))
		if p.isOp(";") {
			p.step()
		} else if k := kw(p.tok); k != "endprocedure" && k != "endfunction" {
			p.fail(p.tok.pos, "expected \";\", found %s", p.tok.describe())
		}
	}
}

func routineWord(r *Routine) string {
	if r.IsFunction {
		return "function"
	}
	return "procedure"
}

func (p *parser) statement(r *Routine) Stmt {
	t := p.tok
	if k := kw(t); k == "return" {
		p.step()
		ret := &Return{Pos: t.pos}
		hasValue := !p.isOp(";") && kw(p.tok) != "endprocedure" && kw(p.tok) != "endfunction"
		if hasValue {
			ret.Value = p.expr()
		}
		switch {
		case r.IsFunction && !hasValue:
			p.fail(t.pos, "a function must return a value")
		case !r.IsFunction && hasValue:
			p.fail(t.pos, "a procedure cannot return a value")
		}
		return ret
	}
	if w := unsupportedWord(t); w != "" {
		p.unsupported(t.pos, "%q statement", t.text)
	}
	if t.kind != kIdent || kw(t) != "" {
		p.fail(t.pos, "expected a statement, found %s", t.describe())
	}
	p.step()
	switch {
	case p.isOp("("):
		call := p.callArgs(t)
		p.rejectSuffix()
		return &CallStmt{Pos: t.pos, Call: call}
	case p.isOp("="):
		p.step()
		return &Assign{Pos: t.pos, Name: t.text, Value: p.expr()}
	case p.isOp(".") || p.isOp("["):
		p.unsupported(p.tok.pos, "member access and indexing")
	}
	p.fail(p.tok.pos, "expected \"=\" or \"(\" after %q, found %s", t.text, p.tok.describe())
	return nil
}

// callArgs читает "(" аргументы ")" после уже взятого имени.
func (p *parser) callArgs(name token) *Call {
	p.expectOp("(")
	c := &Call{Pos: name.pos, Name: name.text}
	if p.isOp(")") {
		p.step()
		return c
	}
	for {
		if p.isOp(",") || p.isOp(")") {
			p.unsupported(p.tok.pos, "omitted call argument")
		}
		c.Args = append(c.Args, p.expr())
		if p.isOp(",") {
			p.step()
			continue
		}
		p.expectOp(")")
		return c
	}
}

func (p *parser) rejectSuffix() {
	if p.isOp(".") || p.isOp("[") {
		p.unsupported(p.tok.pos, "member access and indexing")
	}
}

// Приоритеты (от низшего): или, и, не, сравнение, + -, * / %, унарные + -.
func (p *parser) expr() Expr { return p.orExpr() }

func (p *parser) orExpr() Expr {
	x := p.andExpr()
	for kw(p.tok) == "or" {
		op := p.take()
		x = &Binary{Pos: op.pos, Op: "or", X: x, Y: p.andExpr()}
	}
	return x
}

func (p *parser) andExpr() Expr {
	x := p.notExpr()
	for kw(p.tok) == "and" {
		op := p.take()
		x = &Binary{Pos: op.pos, Op: "and", X: x, Y: p.notExpr()}
	}
	return x
}

func (p *parser) notExpr() Expr {
	if kw(p.tok) == "not" {
		op := p.take()
		return &Unary{Pos: op.pos, Op: "not", X: p.notExpr()}
	}
	return p.cmpExpr()
}

func (p *parser) cmpExpr() Expr {
	x := p.addExpr()
	if p.tok.kind == kOp {
		switch p.tok.text {
		case "=", "<>", "<", ">", "<=", ">=":
			op := p.take()
			return &Binary{Pos: op.pos, Op: op.text, X: x, Y: p.addExpr()}
		}
	}
	return x
}

func (p *parser) addExpr() Expr {
	x := p.mulExpr()
	for p.isOp("+") || p.isOp("-") {
		op := p.take()
		x = &Binary{Pos: op.pos, Op: op.text, X: x, Y: p.mulExpr()}
	}
	return x
}

func (p *parser) mulExpr() Expr {
	x := p.unaryExpr()
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		op := p.take()
		x = &Binary{Pos: op.pos, Op: op.text, X: x, Y: p.unaryExpr()}
	}
	return x
}

func (p *parser) unaryExpr() Expr {
	if p.isOp("-") || p.isOp("+") {
		op := p.take()
		return &Unary{Pos: op.pos, Op: op.text, X: p.unaryExpr()}
	}
	return p.primary()
}

func (p *parser) primary() Expr {
	t := p.tok
	var x Expr
	switch {
	case t.kind == kNumber:
		p.step()
		x = &NumberLit{Pos: t.pos, Value: t.num, Text: t.text}
	case t.kind == kString:
		p.step()
		x = &StringLit{Pos: t.pos, Value: t.text}
	case p.isOp("("):
		p.step()
		x = p.expr()
		p.expectOp(")")
	case p.isOp("?"):
		p.unsupported(t.pos, "ternary operator ?")
	case t.kind == kIdent:
		switch kw(t) {
		case "true":
			p.step()
			x = &ConstLit{Pos: t.pos, Kind: ConstTrue}
		case "false":
			p.step()
			x = &ConstLit{Pos: t.pos, Kind: ConstFalse}
		case "undefined":
			p.step()
			x = &ConstLit{Pos: t.pos, Kind: ConstUndefined}
		case "null":
			p.step()
			x = &ConstLit{Pos: t.pos, Kind: ConstNull}
		case "":
			if unsupportedWord(t) != "" {
				p.unsupported(t.pos, "%q in an expression", t.text)
			}
			p.step()
			if p.isOp("(") {
				x = p.callArgs(t)
			} else {
				x = &Ident{Pos: t.pos, Name: t.text}
			}
		default:
			p.fail(t.pos, "expected an expression, found %s", t.describe())
		}
	default:
		p.fail(t.pos, "expected an expression, found %s", t.describe())
	}
	p.rejectSuffix()
	return x
}
