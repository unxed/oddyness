package vm

import (
	"fmt"
	"strings"

	"github.com/unxed/oddyness/internal/bsl"
)

type opcode uint8

const (
	opConst opcode = iota // a — индекс константы
	opLoad                // a — слот
	opStore               // a — слот, снимает значение со стека
	opPop
	opNeg
	opPlus
	opNot
	opAdd
	opSub
	opMul
	opDiv
	opMod
	opEq
	opNe
	opLt
	opGt
	opLe
	opGe
	opCheckBool
	opJmpFalseKeep // a — адрес; при Ложь переход, значение остаётся
	opJmpTrueKeep
	opJmp      // a — адрес
	opJmpIfNot // a — адрес; снимает условие, оно обязано быть булевым
	opCall     // a — индекс подпрограммы
	opField    // a — константа с именем колонки (в нижнем регистре)
	opIterNext // a — адрес выхода, b — слот коллекции (b+1 — позиция), c — слот переменной
	opRet      // результат — вершина стека
	opRetUndef
)

type instr struct {
	op      opcode
	a, b, c int
	pos     bsl.Pos
}

type function struct {
	name       string
	isFunction bool
	nparams    int
	nslots     int
	code       []instr
	consts     []Value
}

// Program — скомпилированный модуль.
type Program struct {
	funcs []*function
	index map[string]int
}

func fail(pos bsl.Pos, format string, args ...any) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// Compile компилирует модуль. Переменные уровня модуля пока не поддержаны и
// дают ошибку компиляции.
func Compile(m *bsl.Module) (*Program, error) {
	if len(m.Vars) > 0 {
		return nil, fail(m.Vars[0].Position(), "module-level variables are not supported yet")
	}
	p := &Program{index: map[string]int{}}
	routines := map[string]*bsl.Routine{}
	for _, r := range m.Routines {
		key := strings.ToLower(r.Name)
		p.index[key] = len(p.funcs)
		routines[key] = r
		p.funcs = append(p.funcs, &function{name: r.Name, isFunction: r.IsFunction, nparams: len(r.Params)})
	}
	for i, r := range m.Routines {
		c := &compiler{prog: p, routines: routines, fn: p.funcs[i], slots: map[string]int{}, byRef: map[string]bool{}}
		if err := c.routine(r); err != nil {
			return nil, err
		}
	}
	return p, nil
}

type compiler struct {
	prog     *Program
	routines map[string]*bsl.Routine
	fn       *function
	slots    map[string]int
	byRef    map[string]bool
	err      *Error
	loops    []*loopCtx
}

// loopCtx — адреса Прервать, ждущие конца цикла, и адрес Продолжить.
type loopCtx struct {
	start  int
	breaks []int
}

func (c *compiler) emit(op opcode, a int, pos bsl.Pos) int {
	c.fn.code = append(c.fn.code, instr{op: op, a: a, pos: pos})
	return len(c.fn.code) - 1
}

func (c *compiler) constant(v Value) int {
	c.fn.consts = append(c.fn.consts, v)
	return len(c.fn.consts) - 1
}

func (c *compiler) routine(r *bsl.Routine) *Error {
	for _, p := range r.Params {
		c.slots[strings.ToLower(p.Name)] = len(c.slots)
		c.byRef[strings.ToLower(p.Name)] = !p.ByVal
	}
	for _, d := range r.Locals {
		for _, n := range d.Names {
			key := strings.ToLower(n)
			if _, dup := c.slots[key]; dup {
				return fail(d.Position(), "variable %q duplicates a parameter or variable", n)
			}
			c.slots[key] = len(c.slots)
		}
	}
	for _, s := range r.Body {
		c.stmt(s)
		if c.err != nil {
			return c.err
		}
	}
	c.fn.nslots = len(c.slots)
	c.emit(opRetUndef, 0, r.Position())
	return c.err
}

func (c *compiler) stmt(s bsl.Stmt) {
	switch s := s.(type) {
	case *bsl.Assign:
		key := strings.ToLower(s.Name)
		slot, ok := c.slots[key]
		switch {
		case !ok:
			c.fail(s.Position(), "assignment to undeclared variable %q", s.Name)
			return
		case c.byRef[key]:
			c.fail(s.Position(), "assignment to by-reference parameter %q is not supported; declare it with Знач", s.Name)
			return
		}
		c.expr(s.Value)
		c.emit(opStore, slot, s.Position())
	case *bsl.If:
		c.ifStmt(s)
	case *bsl.While:
		c.whileStmt(s)
	case *bsl.ForEach:
		c.forEach(s)
	case *bsl.Break:
		l := c.loops[len(c.loops)-1]
		l.breaks = append(l.breaks, c.emit(opJmp, 0, s.Position()))
	case *bsl.Continue:
		c.emit(opJmp, c.loops[len(c.loops)-1].start, s.Position())
	case *bsl.CallStmt:
		c.call(s.Call, false)
		c.emit(opPop, 0, s.Position())
	case *bsl.Return:
		if s.Value == nil {
			c.emit(opRetUndef, 0, s.Position())
			return
		}
		c.expr(s.Value)
		c.emit(opRet, 0, s.Position())
	}
}

func (c *compiler) block(body []bsl.Stmt) {
	for _, s := range body {
		c.stmt(s)
	}
}

func (c *compiler) patch(at int) { c.fn.code[at].a = len(c.fn.code) }

func (c *compiler) ifStmt(s *bsl.If) {
	var ends []int
	branch := func(cond bsl.Expr, body []bsl.Stmt) {
		c.expr(cond)
		skip := c.emit(opJmpIfNot, 0, cond.Position())
		c.block(body)
		ends = append(ends, c.emit(opJmp, 0, cond.Position()))
		c.patch(skip)
	}
	branch(s.Cond, s.Then)
	for _, e := range s.Elifs {
		branch(e.Cond, e.Body)
	}
	c.block(s.Else)
	for _, e := range ends {
		c.patch(e)
	}
}

func (c *compiler) whileStmt(s *bsl.While) {
	l := &loopCtx{start: len(c.fn.code)}
	c.expr(s.Cond)
	exit := c.emit(opJmpIfNot, 0, s.Cond.Position())
	c.loops = append(c.loops, l)
	c.block(s.Body)
	c.loops = c.loops[:len(c.loops)-1]
	c.emit(opJmp, l.start, s.Position())
	c.patch(exit)
	for _, b := range l.breaks {
		c.patch(b)
	}
}

// forEach: коллекция вычисляется один раз в скрытый слот, рядом — слот позиции.
// Переменная цикла объявляется неявно, если её нет.
func (c *compiler) forEach(s *bsl.ForEach) {
	key := strings.ToLower(s.Var)
	slot, ok := c.slots[key]
	if ok && c.byRef[key] {
		c.fail(s.Position(), "loop variable %q is a by-reference parameter; declare it with Знач", s.Var)
		return
	}
	if !ok {
		slot = len(c.slots)
		c.slots[key] = slot
	}
	hidden := len(c.slots)
	c.slots[fmt.Sprintf("\x00coll%d", hidden)] = hidden
	c.slots[fmt.Sprintf("\x00pos%d", hidden)] = hidden + 1
	c.expr(s.Coll)
	c.emit(opStore, hidden, s.Position())
	c.emit(opConst, c.constant(Value{}), s.Position())
	c.emit(opStore, hidden+1, s.Position())
	l := &loopCtx{start: len(c.fn.code)}
	next := c.emit(opIterNext, 0, s.Position())
	c.fn.code[next].b = hidden
	c.fn.code[next].c = slot
	c.loops = append(c.loops, l)
	c.block(s.Body)
	c.loops = c.loops[:len(c.loops)-1]
	c.emit(opJmp, l.start, s.Position())
	c.patch(next)
	for _, b := range l.breaks {
		c.patch(b)
	}
}

func (c *compiler) fail(pos bsl.Pos, format string, args ...any) {
	if c.err == nil {
		c.err = fail(pos, format, args...)
	}
}

func (c *compiler) call(e *bsl.Call, needValue bool) {
	idx, ok := c.prog.index[strings.ToLower(e.Name)]
	if !ok {
		c.fail(e.Position(), "unknown routine %q", e.Name)
		return
	}
	r := c.routines[strings.ToLower(e.Name)]
	if needValue && !r.IsFunction {
		c.fail(e.Position(), "procedure %q does not return a value", e.Name)
		return
	}
	if len(e.Args) != len(r.Params) {
		c.fail(e.Position(), "%q takes %d arguments, got %d", e.Name, len(r.Params), len(e.Args))
		return
	}
	for _, a := range e.Args {
		c.expr(a)
	}
	c.emit(opCall, idx, e.Position())
}

func (c *compiler) expr(e bsl.Expr) {
	if c.err != nil {
		return
	}
	switch e := e.(type) {
	case *bsl.NumberLit:
		c.emit(opConst, c.constant(NumberValue(e.Value)), e.Position())
	case *bsl.StringLit:
		c.emit(opConst, c.constant(StringValue(e.Value)), e.Position())
	case *bsl.ConstLit:
		var v Value
		switch e.Kind {
		case bsl.ConstTrue:
			v = BoolValue(true)
		case bsl.ConstFalse:
			v = BoolValue(false)
		case bsl.ConstNull:
			v = NullValue()
		}
		c.emit(opConst, c.constant(v), e.Position())
	case *bsl.Ident:
		slot, ok := c.slots[strings.ToLower(e.Name)]
		if !ok {
			c.fail(e.Position(), "unknown name %q", e.Name)
			return
		}
		c.emit(opLoad, slot, e.Position())
	case *bsl.Member:
		slot, ok := c.slots[strings.ToLower(e.Object)]
		if !ok {
			c.fail(e.Position(), "unknown name %q", e.Object)
			return
		}
		c.emit(opLoad, slot, e.Position())
		c.emit(opField, c.constant(StringValue(strings.ToLower(e.Field))), e.Position())
	case *bsl.Call:
		c.call(e, true)
	case *bsl.Unary:
		c.expr(e.X)
		switch e.Op {
		case "-":
			c.emit(opNeg, 0, e.Position())
		case "+":
			c.emit(opPlus, 0, e.Position())
		case "not":
			c.emit(opNot, 0, e.Position())
		}
	case *bsl.Binary:
		c.binary(e)
	}
}

var binaryOps = map[string]opcode{
	"+": opAdd, "-": opSub, "*": opMul, "/": opDiv, "%": opMod, "=": opEq, "<>": opNe,
	"<": opLt, ">": opGt, "<=": opLe, ">=": opGe,
}

func (c *compiler) binary(e *bsl.Binary) {
	switch e.Op {
	case "and", "or":
		jump := opJmpFalseKeep
		if e.Op == "or" {
			jump = opJmpTrueKeep
		}
		c.expr(e.X)
		c.emit(opCheckBool, 0, e.X.Position())
		j := c.emit(jump, 0, e.Position())
		c.emit(opPop, 0, e.Position())
		c.expr(e.Y)
		c.emit(opCheckBool, 0, e.Y.Position())
		c.fn.code[j].a = len(c.fn.code)
	default:
		c.expr(e.X)
		c.expr(e.Y)
		c.emit(binaryOps[e.Op], 0, e.Position())
	}
}
