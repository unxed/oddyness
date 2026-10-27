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

// Run вызывает подпрограмму name (регистр не важен) с аргументами. Проверяется
// отмена ctx в начале и затем каждые 256 инструкций; превышение MaxSteps и
// глубины вызовов — ошибки времени выполнения.
func (p *Program) Run(ctx context.Context, name string, args []Value, opt Options) (Value, error) {
	idx, ok := p.index[strings.ToLower(name)]
	if !ok {
		return Value{}, rtErr(bsl.Pos{}, "unknown routine %q", name)
	}
	fn := p.funcs[idx]
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

	stack := make([]Value, 0, 64)
	stack = append(stack, args...)
	for i := fn.nparams; i < fn.nslots; i++ {
		stack = append(stack, Value{})
	}
	frames := []frame{{fn: fn}}
	var steps int64

	pop := func() Value {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v
	}
	for {
		f := &frames[len(frames)-1]
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
			stack = append(stack, f.fn.consts[in.a])
		case opLoad:
			stack = append(stack, stack[f.bp+in.a])
		case opStore:
			stack[f.bp+in.a] = pop()
		case opPop:
			pop()
		case opNeg:
			v := pop()
			if v.kind != Number {
				return Value{}, rtErr(in.pos, "cannot negate %s", v.kind)
			}
			stack = append(stack, NumberValue(v.num.Neg()))
		case opPlus:
			if v := stack[len(stack)-1]; v.kind != Number {
				return Value{}, rtErr(in.pos, "unary + needs a Number, got %s", v.kind)
			}
		case opNot:
			v := pop()
			if v.kind != Bool {
				return Value{}, rtErr(in.pos, "Not needs a Boolean, got %s", v.kind)
			}
			stack = append(stack, BoolValue(!v.b))
		case opCheckBool:
			if v := stack[len(stack)-1]; v.kind != Bool {
				return Value{}, rtErr(in.pos, "And/Or operand must be Boolean, got %s", v.kind)
			}
		case opJmpFalseKeep:
			if !stack[len(stack)-1].b {
				f.pc = in.a
			}
		case opJmpTrueKeep:
			if stack[len(stack)-1].b {
				f.pc = in.a
			}
		case opAdd, opSub, opMul, opDiv, opMod:
			b, a := pop(), pop()
			r, err := arith(in, a, b)
			if err != nil {
				return Value{}, err
			}
			stack = append(stack, r)
		case opEq, opNe:
			b, a := pop(), pop()
			eq := equal(a, b)
			stack = append(stack, BoolValue(eq == (in.op == opEq)))
		case opLt, opGt, opLe, opGe:
			b, a := pop(), pop()
			r, err := order(in, a, b)
			if err != nil {
				return Value{}, err
			}
			stack = append(stack, r)
		case opCall:
			if len(frames) >= MaxCallDepth {
				return Value{}, rtErr(in.pos, "call depth limit %d exceeded", MaxCallDepth)
			}
			callee := p.funcs[in.a]
			bp := len(stack) - callee.nparams
			for i := callee.nparams; i < callee.nslots; i++ {
				stack = append(stack, Value{})
			}
			frames = append(frames, frame{fn: callee, bp: bp})
		case opRet, opRetUndef:
			var result Value
			if in.op == opRet {
				result = pop()
			}
			stack = append(stack[:f.bp], result)
			frames = frames[:len(frames)-1]
			if len(frames) == 0 {
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
