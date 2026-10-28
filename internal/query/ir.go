package query

import (
	"fmt"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

// Kind — тип значения в запросе.
type Kind int

const (
	KindNumber Kind = iota + 1
	KindString
	KindBool
	KindDate
	KindRef
)

// Type — тип поля, константы или параметра. Target — id справочника у ссылки.
type Type struct {
	Kind   Kind
	Target string
}

func (t Type) String() string {
	switch t.Kind {
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindBool:
		return "boolean"
	case KindDate:
		return "date"
	case KindRef:
		return "ref(" + t.Target + ")"
	}
	return "invalid"
}

func typeOf(f metadata.Field) Type {
	switch f.Type {
	case metadata.TypeNumber:
		return Type{Kind: KindNumber}
	case metadata.TypeString:
		return Type{Kind: KindString}
	case metadata.TypeBoolean:
		return Type{Kind: KindBool}
	case metadata.TypeDate:
		return Type{Kind: KindDate}
	}
	return Type{Kind: KindRef, Target: f.Target}
}

// Role — роль поля в виртуальной таблице остатков.
type Role int

const (
	Dimension Role = iota + 1 // измерение: по нему группируются остатки
	Resource                  // ресурс: сумма движений по сочетанию измерений
)

// FieldRef — поле виртуальной таблицы остатков, проверенное по метаданным регистра.
type FieldRef struct {
	ID   string // id поля в метаданных (ключ значений Store.Balances)
	Name string
	Role Role
	Type Type
}

// Query — разобранный и проверенный запрос к остаткам одного регистра накопления.
type Query struct {
	Register string // id регистра
	Columns  []Column
	Where    Expr // nil — без отбора
	Order    []OrderKey
	Params   []Param // по первому упоминанию
}

// Column — столбец результата: имя в результате (псевдоним или имя поля) и исходное поле.
type Column struct {
	Name  string
	Field FieldRef
	Pos   Pos
}

// OrderKey — ключ упорядочивания: индекс столбца результата.
type OrderKey struct {
	Column int
	Desc   bool
	Pos    Pos
}

// Param — параметр &Имя с типом, выведенным из его сравнения с полем.
type Param struct {
	Name string // написание первого упоминания; поиск без учёта регистра
	Type Type
	Pos  Pos
}

// CmpOp — оператор сравнения.
type CmpOp string

const (
	OpEq CmpOp = "="
	OpNe CmpOp = "<>"
	OpLt CmpOp = "<"
	OpLe CmpOp = "<="
	OpGt CmpOp = ">"
	OpGe CmpOp = ">="
)

// Expr — условие отбора: And, Or, Not или Cmp.
type Expr interface{ expr() }

type (
	And struct{ Left, Right Expr }
	Or  struct{ Left, Right Expr }
	Not struct{ X Expr }
	Cmp struct {
		Op          CmpOp
		Left, Right Operand
		Pos         Pos
	}
)

func (And) expr() {}
func (Or) expr()  {}
func (Not) expr() {}
func (Cmp) expr() {}

// Operand — сторона сравнения: FieldOperand, ParamOperand или Const.
type Operand interface{ operandType() Type }

// FieldOperand — поле таблицы остатков.
type FieldOperand struct{ Field FieldRef }

// ParamOperand — параметр; Index — позиция в Query.Params.
type ParamOperand struct {
	Index int
	Type  Type
}

// Const — литерал: число (точное value.Number), строка или булево.
type Const struct {
	Type Type
	Num  value.Number
	Str  string
	Bool bool
}

func (o FieldOperand) operandType() Type { return o.Field.Type }
func (o ParamOperand) operandType() Type { return o.Type }
func (c Const) operandType() Type        { return c.Type }

// ErrParam — ошибка привязки параметров запроса.
type ErrParam struct{ Msg string }

func (e *ErrParam) Error() string { return "query: " + e.Msg }

func paramErr(format string, args ...any) error { return &ErrParam{Msg: fmt.Sprintf(format, args...)} }
