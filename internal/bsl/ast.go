package bsl

import "github.com/unxed/oddyness/internal/value"

// Module — разобранный модуль: переменные уровня модуля и подпрограммы.
type Module struct {
	Vars     []*VarDecl
	Routines []*Routine
}

// VarDecl — `Перем a, b [Экспорт];`.
type VarDecl struct {
	Pos
	Names  []string
	Export bool
}

// Param — параметр подпрограммы; ByVal — `Знач`.
type Param struct {
	Pos
	Name  string
	ByVal bool
}

// Routine — процедура или функция. Имена хранятся как написаны в тексте;
// сравнение имён регистронезависимо.
type Routine struct {
	Pos
	Name       string
	IsFunction bool
	Export     bool
	Params     []Param
	Locals     []*VarDecl
	Body       []Stmt
}

// Stmt — оператор.
type Stmt interface {
	Position() Pos
	stmt()
}

// Expr — выражение.
type Expr interface {
	Position() Pos
	expr()
}

type (
	// Assign — `имя = выражение;`.
	Assign struct {
		Pos
		Name  string
		Value Expr
	}
	// CallStmt — вызов как оператор.
	CallStmt struct {
		Pos
		Call *Call
	}
	// Return — `Возврат [выражение];`.
	Return struct {
		Pos
		Value Expr // nil для `Возврат;`
	}
)

type (
	// If — Если/ИначеЕсли/Иначе/КонецЕсли.
	If struct {
		Pos
		Cond  Expr
		Then  []Stmt
		Elifs []ElseIf
		Else  []Stmt // nil, если ветки Иначе нет
	}
	// ElseIf — ветка ИначеЕсли.
	ElseIf struct {
		Pos
		Cond Expr
		Body []Stmt
	}
	// While — Пока … Цикл … КонецЦикла.
	While struct {
		Pos
		Cond Expr
		Body []Stmt
	}
	// Break — Прервать; Continue — Продолжить (только внутри цикла).
	Break    struct{ Pos }
	Continue struct{ Pos }
)

func (*If) stmt()       {}
func (*While) stmt()    {}
func (*Break) stmt()    {}
func (*Continue) stmt() {}
func (*Assign) stmt()   {}
func (*CallStmt) stmt() {}
func (*Return) stmt()   {}

// ConstKind — ключевое литеральное значение.
type ConstKind int

const (
	ConstTrue ConstKind = iota
	ConstFalse
	ConstUndefined
	ConstNull
)

type (
	// NumberLit — точный числовой литерал; Text — исходная запись.
	NumberLit struct {
		Pos
		Value value.Number
		Text  string
	}
	// StringLit — строка после снятия кавычек и склейки продолжений.
	StringLit struct {
		Pos
		Value string
	}
	// ConstLit — Истина, Ложь, Неопределено, Null.
	ConstLit struct {
		Pos
		Kind ConstKind
	}
	// Ident — обращение к переменной или параметру.
	Ident struct {
		Pos
		Name string
	}
	// Call — вызов подпрограммы по имени.
	Call struct {
		Pos
		Name string
		Args []Expr
	}
	// Unary — операторы "-", "+", "not".
	Unary struct {
		Pos
		Op string
		X  Expr
	}
	// Binary — "+", "-", "*", "/", "%", "=", "<>", "<", ">", "<=", ">=", "and", "or".
	Binary struct {
		Pos
		Op   string
		X, Y Expr
	}
)

func (*NumberLit) expr() {}
func (*StringLit) expr() {}
func (*ConstLit) expr()  {}
func (*Ident) expr()     {}
func (*Call) expr()      {}
func (*Unary) expr()     {}
func (*Binary) expr()    {}
