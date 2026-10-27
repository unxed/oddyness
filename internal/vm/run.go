package vm

import (
	"context"
	"strings"

	"github.com/unxed/oddyness/internal/bsl"
)

const (
	// DefaultMaxSteps — предел инструкций, если Options.MaxSteps не задан.
	DefaultMaxSteps = 10_000_000
	// MaxCallDepth — предельная глубина вложенных вызовов.
	MaxCallDepth = 256
)

// Options ограничивают выполнение. Нулевые значения означают пределы по умолчанию.
type Options struct {
	MaxSteps int64
	// Host выполняет функцию хоста по имени, как оно объявлено в HostDecl.
	// Ошибка прерывает выполнение BSL ошибкой времени выполнения с позицией вызова.
	Host func(name string, args []Value) (Value, error)
}

type frame struct {
	fn     *function
	pc, bp int
}

func rtErr(pos bsl.Pos, format string, args ...any) *Error {
	e := fail(pos, format, args...)
	e.Runtime = true
	return e
}

// Machine выполняет программу и хранит буферы стека и кадров между вызовами:
// после прогрева повторный Call не выделяет память под них. Машина не
// потокобезопасна; на каждый поток — своя.
type Machine struct {
	p      *Program
	stack  []Value
	frames []frame
}

// NewMachine создаёт машину для программы.
func (p *Program) NewMachine() *Machine {
	return &Machine{p: p, stack: make([]Value, 0, 64), frames: make([]frame, 0, 16)}
}

// Entry — разрешённая точка входа: имя ищется один раз.
type Entry struct{ idx int }

// NumParams возвращает число параметров точки входа.
func (p *Program) NumParams(e Entry) int { return p.funcs[e.idx].nparams }

// Entry находит подпрограмму по имени (регистр не важен).
func (p *Program) Entry(name string) (Entry, error) {
	idx, ok := p.index[strings.ToLower(name)]
	if !ok {
		return Entry{}, rtErr(bsl.Pos{}, "unknown routine %q", name)
	}
	return Entry{idx}, nil
}

// Run вызывает подпрограмму name (регистр не важен) на новой машине.
func (p *Program) Run(ctx context.Context, name string, args []Value, opt Options) (Value, error) {
	e, err := p.Entry(name)
	if err != nil {
		return Value{}, err
	}
	return p.NewMachine().Call(ctx, e, args, opt)
}

func (m *Machine) pop() Value {
	v := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	return v
}

// Call вызывает подпрограмму с аргументами. Проверяется отмена ctx в начале и
// затем каждые 256 инструкций; превышение MaxSteps и глубины вызовов — ошибки
// времени выполнения. Значения аргументов копируются, таблицы — по ссылке.
func (m *Machine) Call(ctx context.Context, e Entry, args []Value, opt Options) (Value, error) {
	fn := m.p.funcs[e.idx]
	if len(args) != fn.nparams {
		return Value{}, rtErr(bsl.Pos{}, "%q takes %d arguments, got %d", fn.name, fn.nparams, len(args))
	}
	if err := ctx.Err(); err != nil {
		return Value{}, rtErr(bsl.Pos{}, "cancelled: %v", err)
	}
	maxSteps := opt.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	m.stack = append(m.stack[:0], args...)
	for i := fn.nparams; i < fn.nslots; i++ {
		m.stack = append(m.stack, Value{})
	}
	m.frames = append(m.frames[:0], frame{fn: fn})
	var steps int64

	for {
		f := &m.frames[len(m.frames)-1]
		in := f.fn.code[f.pc]
		f.pc++
		steps++
		if steps > maxSteps {
			return Value{}, rtErr(in.pos, "step limit %d exceeded", maxSteps)
		}
		if steps&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, rtErr(in.pos, "cancelled: %v", err)
			}
		}
		switch in.op {
		case opConst:
			m.stack = append(m.stack, f.fn.consts[in.a])
		case opLoad:
			m.stack = append(m.stack, m.stack[f.bp+in.a])
		case opStore:
			m.stack[f.bp+in.a] = m.pop()
		case opPop:
			m.pop()
		case opNeg:
			v := m.pop()
			if v.kind != Number {
				return Value{}, rtErr(in.pos, "cannot negate %s", v.kind)
			}
			m.stack = append(m.stack, NumberValue(v.num.Neg()))
		case opPlus:
			if v := m.stack[len(m.stack)-1]; v.kind != Number {
				return Value{}, rtErr(in.pos, "unary + needs a Number, got %s", v.kind)
			}
		case opNot:
			v := m.pop()
			if v.kind != Bool {
				return Value{}, rtErr(in.pos, "Not needs a Boolean, got %s", v.kind)
			}
			m.stack = append(m.stack, BoolValue(!v.b))
		case opCheckBool:
			if v := m.stack[len(m.stack)-1]; v.kind != Bool {
				return Value{}, rtErr(in.pos, "And/Or operand must be Boolean, got %s", v.kind)
			}
		case opJmpFalseKeep:
			if !m.stack[len(m.stack)-1].b {
				f.pc = in.a
			}
		case opJmp:
			f.pc = in.a
		case opJmpIfNot:
			v := m.pop()
			if v.kind != Bool {
				return Value{}, rtErr(in.pos, "condition must be Boolean, got %s", v.kind)
			}
			if !v.b {
				f.pc = in.a
			}
		case opIterNext:
			coll := m.stack[f.bp+in.b]
			if coll.kind != Collection {
				return Value{}, rtErr(in.pos, "For Each needs a collection, got %s", coll.kind)
			}
			pos := &m.stack[f.bp+in.b+1]
			if pos.i >= len(coll.tbl.rows) {
				f.pc = in.a
			} else {
				m.stack[f.bp+in.c] = Value{kind: Row, tbl: coll.tbl, i: pos.i}
				pos.i++
			}
		case opField:
			row := &m.stack[len(m.stack)-1]
			if row.kind != Row {
				return Value{}, rtErr(in.pos, "cannot read field %q of %s", f.fn.consts[in.a].str, row.kind)
			}
			col, ok := row.tbl.index[f.fn.consts[in.a].str]
			if !ok {
				return Value{}, rtErr(in.pos, "unknown column %q", f.fn.consts[in.a].str)
			}
			*row = row.tbl.rows[row.i][col]
		case opJmpTrueKeep:
			if m.stack[len(m.stack)-1].b {
				f.pc = in.a
			}
		case opAdd, opSub, opMul, opDiv, opMod:
			b, a := m.pop(), m.pop()
			r, err := arith(in, a, b)
			if err != nil {
				return Value{}, err
			}
			m.stack = append(m.stack, r)
		case opEq, opNe:
			b, a := m.pop(), m.pop()
			eq := equal(a, b)
			m.stack = append(m.stack, BoolValue(eq == (in.op == opEq)))
		case opLt, opGt, opLe, opGe:
			b, a := m.pop(), m.pop()
			r, err := order(in, a, b)
			if err != nil {
				return Value{}, err
			}
			m.stack = append(m.stack, r)
		case opHost:
			if opt.Host == nil {
				return Value{}, rtErr(in.pos, "host function %q is not available", m.p.host[in.a].Name)
			}
			args := make([]Value, in.b)
			copy(args, m.stack[len(m.stack)-in.b:])
			m.stack = m.stack[:len(m.stack)-in.b]
			res, err := opt.Host(m.p.host[in.a].Name, args)
			if err != nil {
				return Value{}, rtErr(in.pos, "%s: %v", m.p.host[in.a].Name, err)
			}
			m.stack = append(m.stack, res)
		case opCall:
			if len(m.frames) >= MaxCallDepth {
				return Value{}, rtErr(in.pos, "call depth limit %d exceeded", MaxCallDepth)
			}
			callee := m.p.funcs[in.a]
			bp := len(m.stack) - callee.nparams
			for i := callee.nparams; i < callee.nslots; i++ {
				m.stack = append(m.stack, Value{})
			}
			m.frames = append(m.frames, frame{fn: callee, bp: bp})
		case opRet, opRetUndef:
			var result Value
			if in.op == opRet {
				result = m.pop()
			}
			m.stack = append(m.stack[:f.bp], result)
			m.frames = m.frames[:len(m.frames)-1]
			if len(m.frames) == 0 {
				return result, nil
			}
		}
	}
}

func arith(in instr, a, b Value) (Value, error) {
	switch {
	case a.kind == Number && b.kind == Number:
		var r Value
		var err error
		switch in.op {
		case opAdd:
			r.num, err = a.num.Add(b.num)
		case opSub:
			r.num, err = a.num.Sub(b.num)
		case opMul:
			r.num, err = a.num.Mul(b.num)
		case opDiv:
			r.num, err = a.num.Div(b.num)
		default:
			r.num, err = a.num.Mod(b.num)
		}
		if err != nil {
			return Value{}, rtErr(in.pos, "%v", err)
		}
		r.kind = Number
		return r, nil
	case in.op == opAdd && a.kind == String && b.kind == String:
		return StringValue(a.str + b.str), nil
	}
	verb := map[opcode]string{opAdd: "add", opSub: "subtract", opMul: "multiply", opDiv: "divide", opMod: "take the remainder of"}[in.op]
	return Value{}, rtErr(in.pos, "cannot %s %s and %s", verb, a.kind, b.kind)
}

// equal: значения разных типов не равны (без ошибки); Number сравниваются по
// значению независимо от масштаба.
func equal(a, b Value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case Bool:
		return a.b == b.b
	case Number:
		return a.num.Cmp(b.num) == 0
	case String:
		return a.str == b.str
	case Reference:
		return a.str == b.str && a.i == b.i
	}
	return true // Неопределено = Неопределено, Null = Null
}

// order: упорядочиваются только Number с Number и String со String (по байтам
// UTF-8, не по правилам культуры); остальное — ошибка типа.
func order(in instr, a, b Value) (Value, error) {
	var c int
	switch {
	case a.kind == Number && b.kind == Number:
		c = a.num.Cmp(b.num)
	case a.kind == String && b.kind == String:
		c = strings.Compare(a.str, b.str)
	default:
		return Value{}, rtErr(in.pos, "cannot compare %s and %s", a.kind, b.kind)
	}
	var r bool
	switch in.op {
	case opLt:
		r = c < 0
	case opGt:
		r = c > 0
	case opLe:
		r = c <= 0
	case opGe:
		r = c >= 0
	}
	return BoolValue(r), nil
}
