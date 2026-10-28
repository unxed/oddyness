package query

import (
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

// Ключевые слова (русские и английские формы). Регистр букв не важен. Слова из core зарезервированы:
// поле с таким именем в запросе не выбрать.
var core = map[string]string{
	"select": "select", "выбрать": "select",
	"from": "from", "из": "from",
	"where": "where", "где": "where",
	"and": "and", "и": "and",
	"or": "or", "или": "or",
	"not": "not", "не": "not",
	"order": "order", "упорядочить": "order",
	"by": "by", "по": "by",
	"as": "as", "как": "as",
	"asc": "asc", "возр": "asc",
	"desc": "desc", "убыв": "desc",
	"true": "true", "истина": "true",
	"false": "false", "ложь": "false",
}

// Слова языка запросов 1С и SQL, которых в подмножестве нет: встретив их, разбор отвечает «не поддерживается».
var unsupportedWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`join соединение left левое right правое inner внутреннее full полное outer внешнее
		group сгруппировать having имеющие union объединить distinct различные top первые in в like подобно
		between между totals итоги into поместить allowed разрешенные limit offset exists существует
		case выбор when когда then тогда else иначе end конец is есть null all все with over`) {
		unsupportedWords[w] = true
	}
}

type rawItem struct {
	name, alias   string
	pos, aliasPos Pos
}

type rawOperand struct {
	kind byte // 'i' ident, 'p' param, 'n' number, 's' string, 'b' bool
	text string
	num  value.Number
	b    bool
	pos  Pos
}

type rawExpr interface{}

type (
	rawBin struct {
		op          string // "and" | "or"
		left, right rawExpr
	}
	rawNot struct{ x rawExpr }
	rawCmp struct {
		op          CmpOp
		left, right rawOperand
		pos         Pos
	}
)

type rawKey struct {
	name string
	desc bool
	pos  Pos
}

type rawQuery struct {
	items  []rawItem
	source [3]string
	srcPos [3]Pos
	where  rawExpr
	order  []rawKey
}

type parser struct {
	lx  *lexer
	tok token
}

func (p *parser) next() {
	t, err := p.lx.scan()
	if err != nil {
		panic(err)
	}
	p.tok = t
}

func (p *parser) isKw(k string) bool {
	return p.tok.kind == tIdent && core[strings.ToLower(p.tok.text)] == k
}

func (p *parser) isOp(s string) bool { return p.tok.kind == tOp && p.tok.text == s }

func (p *parser) unexpected() {
	t := p.tok
	switch {
	case t.kind == tEOF:
		panic(fail(t.pos, false, "unexpected end of query"))
	case t.kind == tIdent && unsupportedWords[strings.ToLower(t.text)]:
		panic(fail(t.pos, true, "%s is not supported", t.text))
	case t.kind == tOp && strings.Contains("+-*/%", t.text) && t.text != "":
		panic(fail(t.pos, true, "operator %s is not supported (no arithmetic in queries)", t.text))
	case t.kind == tOp && (t.text == "[" || t.text == "]" || t.text == "?"):
		panic(fail(t.pos, true, "%s is not supported", t.text))
	}
	panic(fail(t.pos, false, "unexpected %s", p.describe()))
}

func (p *parser) describe() string {
	switch p.tok.kind {
	case tString:
		return "string literal"
	case tNumber:
		return "number " + p.tok.text
	case tParam:
		return "parameter &" + p.tok.text
	}
	return `"` + p.tok.text + `"`
}

func (p *parser) expectKw(k string) {
	if !p.isKw(k) {
		p.unexpected()
	}
	p.next()
}

// name читает имя поля: не зарезервированное слово, без квалификатора и вызова.
func (p *parser) name() (string, Pos) {
	t := p.tok
	if t.kind != tIdent || core[strings.ToLower(t.text)] != "" {
		p.unexpected()
	}
	if unsupportedWords[strings.ToLower(t.text)] {
		p.unexpected()
	}
	p.next()
	p.afterName(t)
	return t.text, t.pos
}

func (p *parser) afterName(t token) {
	if p.isOp("(") {
		panic(fail(t.pos, true, "function calls are not supported: %s(...)", t.text))
	}
	if p.isOp(".") {
		panic(fail(p.tok.pos, true, "qualified names are not supported: use the plain field name"))
	}
}

func parseRaw(src string) (rq *rawQuery, err *Error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*Error)
			if !ok {
				panic(r)
			}
			rq, err = nil, e
		}
	}()
	p := &parser{lx: newLexer(src)}
	p.next()
	rq = &rawQuery{}
	p.expectKw("select")
	if p.isOp("*") {
		panic(fail(p.tok.pos, true, "SELECT * is not supported: list the fields"))
	}
	for {
		name, pos := p.name()
		it := rawItem{name: name, pos: pos}
		if p.isKw("as") {
			p.next()
			it.alias, it.aliasPos = p.name()
		}
		rq.items = append(rq.items, it)
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	p.expectKw("from")
	for i := 0; i < 3; i++ {
		if i > 0 {
			if !p.isOp(".") {
				panic(fail(p.tok.pos, false, "source must be AccumulationRegister.<name>.Balance (три части через точку)"))
			}
			p.next()
		}
		if p.tok.kind != tIdent {
			p.unexpected()
		}
		rq.source[i], rq.srcPos[i] = p.tok.text, p.tok.pos
		p.next()
	}
	if p.isKw("where") {
		p.next()
		rq.where = p.or()
	}
	if p.isKw("order") {
		p.next()
		p.expectKw("by")
		for {
			name, pos := p.name()
			k := rawKey{name: name, pos: pos}
			if p.isKw("desc") {
				k.desc = true
				p.next()
			} else if p.isKw("asc") {
				p.next()
			}
			rq.order = append(rq.order, k)
			if !p.isOp(",") {
				break
			}
			p.next()
		}
	}
	if p.tok.kind != tEOF {
		p.unexpected()
	}
	return rq, nil
}

func (p *parser) or() rawExpr {
	l := p.and()
	for p.isKw("or") {
		p.next()
		l = rawBin{op: "or", left: l, right: p.and()}
	}
	return l
}

func (p *parser) and() rawExpr {
	l := p.not()
	for p.isKw("and") {
		p.next()
		l = rawBin{op: "and", left: l, right: p.not()}
	}
	return l
}

func (p *parser) not() rawExpr {
	if p.isKw("not") {
		p.next()
		return rawNot{x: p.not()}
	}
	return p.cmp()
}

func (p *parser) cmp() rawExpr {
	if p.isOp("(") {
		p.next()
		e := p.or()
		if !p.isOp(")") {
			p.unexpected()
		}
		p.next()
		return e
	}
	l := p.operand()
	pos := p.tok.pos
	var op CmpOp
	if p.tok.kind == tOp {
		switch p.tok.text {
		case "=", "<>", "<", "<=", ">", ">=":
			op = CmpOp(p.tok.text)
		}
	}
	if op == "" {
		if p.tok.kind == tEOF || p.tok.kind == tIdent && unsupportedWords[strings.ToLower(p.tok.text)] || p.tok.kind == tOp && strings.Contains("+-*/%", p.tok.text) {
			p.unexpected()
		}
		panic(fail(p.tok.pos, false, "comparison operator (=, <>, <, <=, >, >=) expected, got %s", p.describe()))
	}
	p.next()
	r := p.operand()
	return rawCmp{op: op, left: l, right: r, pos: pos}
}

func (p *parser) operand() rawOperand {
	t := p.tok
	switch t.kind {
	case tParam:
		p.next()
		return rawOperand{kind: 'p', text: t.text, pos: t.pos}
	case tNumber:
		p.next()
		return rawOperand{kind: 'n', text: t.text, num: t.num, pos: t.pos}
	case tString:
		p.next()
		return rawOperand{kind: 's', text: t.text, pos: t.pos}
	case tOp:
		if t.text == "-" {
			p.next()
			if p.tok.kind != tNumber {
				p.unexpected()
			}
			n := p.tok
			p.next()
			return rawOperand{kind: 'n', text: "-" + n.text, num: n.num.Neg(), pos: t.pos}
		}
	case tIdent:
		switch core[strings.ToLower(t.text)] {
		case "true", "false":
			p.next()
			return rawOperand{kind: 'b', b: core[strings.ToLower(t.text)] == "true", pos: t.pos}
		}
		name, pos := p.name()
		return rawOperand{kind: 'i', text: name, pos: pos}
	}
	p.unexpected()
	return rawOperand{}
}

// Parse разбирает текст запроса и проверяет его по метаданным: регистр, поля, типы сравнений и параметров.
// Первая найденная ошибка возвращается как *Error с позицией строка:колонка.
func Parse(src string, model *metadata.Model) (*Query, error) {
	rq, perr := parseRaw(src)
	if perr != nil {
		return nil, perr
	}
	q, err := resolve(rq, model)
	if err != nil {
		return nil, err
	}
	return q, nil
}
