// Package vm компилирует разобранный BSL (internal/bsl) в компактный байткод и
// выполняет его небольшой стековой машиной. Имена разрешаются при компиляции в
// слоты; неизвестное имя, неверное число аргументов и неподдерживаемое
// присваивание — ошибки компиляции с позицией, а не поведение времени выполнения.
package vm

import (
	"fmt"

	"github.com/unxed/oddyness/internal/bsl"
	"github.com/unxed/oddyness/internal/value"
)

// Kind — тип значения BSL.
type Kind uint8

const (
	Undefined Kind = iota
	Null
	Bool
	Number
	String
)

func (k Kind) String() string {
	switch k {
	case Undefined:
		return "Undefined"
	case Null:
		return "Null"
	case Bool:
		return "Boolean"
	case Number:
		return "Number"
	case String:
		return "String"
	}
	return "?"
}

// Value — значение BSL. Нулевое значение — Неопределено.
type Value struct {
	kind Kind
	b    bool
	num  value.Number
	str  string
}

// Конструкторы значений.
func BoolValue(b bool) Value           { return Value{kind: Bool, b: b} }
func NumberValue(n value.Number) Value { return Value{kind: Number, num: n} }
func StringValue(s string) Value       { return Value{kind: String, str: s} }
func NullValue() Value                 { return Value{kind: Null} }
func UndefinedValue() Value            { return Value{} }
func (v Value) Kind() Kind             { return v.kind }
func (v Value) Bool() bool             { return v.b }
func (v Value) Number() value.Number   { return v.num }
func (v Value) StringValue() string    { return v.str }

// String печатает значение так, как его показывает `bsl run`.
func (v Value) String() string {
	switch v.kind {
	case Bool:
		if v.b {
			return "Истина"
		}
		return "Ложь"
	case Number:
		return v.num.String()
	case String:
		return v.str
	case Null:
		return "Null"
	}
	return "Неопределено"
}

// Error — ошибка компиляции или выполнения; Pos нулевая, если позиции нет.
type Error struct {
	Pos     bsl.Pos
	Msg     string
	Runtime bool
}

func (e *Error) Error() string {
	prefix := "compile error: "
	if e.Runtime {
		prefix = "runtime error: "
	}
	if e.Pos.Line == 0 {
		return prefix + e.Msg
	}
	return fmt.Sprintf("%s%d:%d: %s", prefix, e.Pos.Line, e.Pos.Col, e.Msg)
}
