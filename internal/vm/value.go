// Package vm компилирует разобранный BSL (internal/bsl) в компактный байткод и
// выполняет его небольшой стековой машиной. Имена разрешаются при компиляции в
// слоты; неизвестное имя, неверное число аргументов и неподдерживаемое
// присваивание — ошибки компиляции с позицией, а не поведение времени выполнения.
package vm

import (
	"fmt"
	"strings"

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
	// Collection — таблица, переданная хостом (Table); Row — её строка.
	Collection
	Row
	// Reference — ссылка на запись справочника (каталог и номер); BSL её только
	// хранит, сравнивает на равенство и передаёт хосту.
	Reference
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
	case Collection:
		return "Collection"
	case Row:
		return "Row"
	case Reference:
		return "Reference"
	}
	return "?"
}

// Value — значение BSL. Нулевое значение — Неопределено.
type Value struct {
	kind Kind
	b    bool
	num  value.Number
	str  string
	tbl  *Table // Collection и Row
	i    int    // Row: номер строки; скрытые слоты итератора Для Каждого: позиция
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
	case Collection:
		return fmt.Sprintf("Collection(%d rows)", len(v.tbl.rows))
	case Row:
		return fmt.Sprintf("Row(%d)", v.i)
	case Reference:
		return fmt.Sprintf("%s#%d", v.str, v.i)
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

// Table — неизменяемая таблица, которую хост передаёт в BSL (например, табличная
// часть документа). Это весь доступ BSL к данным хоста: перебор строк
// `Для Каждого` и чтение полей `Строка.Колонка`. Имена колонок разрешены
// заранее, регистр не важен; записи в таблицу, индексов и вызовов методов нет.
type Table struct {
	cols  []string
	index map[string]int
	rows  [][]Value
}

// NewTable проверяет таблицу: непустые различающиеся (без учёта регистра)
// имена колонок и строки нужной длины. Данные копируются.
func NewTable(columns []string, rows [][]Value) (*Table, error) {
	t := &Table{cols: append([]string(nil), columns...), index: make(map[string]int, len(columns))}
	for i, c := range columns {
		key := strings.ToLower(c)
		if key == "" {
			return nil, fmt.Errorf("vm: column %d has an empty name", i)
		}
		if _, dup := t.index[key]; dup {
			return nil, fmt.Errorf("vm: duplicate column %q", c)
		}
		t.index[key] = i
	}
	for i, r := range rows {
		if len(r) != len(columns) {
			return nil, fmt.Errorf("vm: row %d has %d values, want %d", i, len(r), len(columns))
		}
		t.rows = append(t.rows, append([]Value(nil), r...))
	}
	return t, nil
}

// RefValue создаёт ссылку на запись справочника.
func RefValue(catalog string, id int64) Value {
	return Value{kind: Reference, str: catalog, i: int(id)}
}

// RefParts возвращает каталог и номер записи ссылки.
func (v Value) RefParts() (catalog string, id int64) { return v.str, int64(v.i) }

// NewRow создаёт строку с одной записью: columns — имена полей, values — их значения
// (значением поля может быть и таблица). Так хост передаёт в BSL объект целиком.
func NewRow(columns []string, values []Value) (Value, error) {
	t, err := NewTable(columns, [][]Value{values})
	if err != nil {
		return Value{}, err
	}
	return Value{kind: Row, tbl: t, i: 0}, nil
}

// Len возвращает число строк.
func (t *Table) Len() int { return len(t.rows) }

// TableValue оборачивает таблицу в значение BSL.
func TableValue(t *Table) Value { return Value{kind: Collection, tbl: t} }
