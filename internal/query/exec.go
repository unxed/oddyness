package query

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

// BalanceReader — чтение остатков ядра; его реализует *sqlite.Store и *sqlite.Tx (через обёртку).
type BalanceReader interface {
	Balances(ctx context.Context, registerID string, q sqlite.BalanceQuery) ([]sqlite.Balance, error)
}

// Result — результат запроса: имена столбцов и строки; значения — value.Number, string, bool, value.Ref или nil.
type Result struct {
	Columns []string
	Rows    [][]any
}

// Run связывает параметры и выполняет запрос над остатками, прочитанными API ядра (Store.Balances).
// Отбор, упорядочивание и сравнения выполняются здесь точным value.Number; SQL запрос не порождает.
func (q *Query) Run(ctx context.Context, r BalanceReader, params map[string]any) (*Result, error) {
	bound, err := q.Bind(params)
	if err != nil {
		return nil, err
	}
	all, err := r.Balances(ctx, q.Register, sqlite.BalanceQuery{})
	if err != nil {
		return nil, err
	}
	res := &Result{}
	for _, c := range q.Columns {
		res.Columns = append(res.Columns, c.Name)
	}
	for _, b := range all {
		if q.Where != nil {
			ok, err := q.eval(q.Where, b, bound)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		row := make([]any, len(q.Columns))
		for i, c := range q.Columns {
			row[i] = fieldValue(c.Field, b)
		}
		res.Rows = append(res.Rows, row)
	}
	if len(q.Order) > 0 {
		sort.SliceStable(res.Rows, func(i, j int) bool {
			for _, k := range q.Order {
				c := compare(res.Rows[i][k.Column], res.Rows[j][k.Column])
				if k.Desc {
					c = -c
				}
				if c != 0 {
					return c < 0
				}
			}
			return false
		})
	}
	return res, nil
}

func fieldValue(f FieldRef, b sqlite.Balance) any {
	if f.Role == Resource {
		return b.Resources[f.ID]
	}
	return b.Dimensions[f.ID]
}

func (q *Query) eval(e Expr, b sqlite.Balance, params map[string]any) (bool, error) {
	switch e := e.(type) {
	case And:
		l, err := q.eval(e.Left, b, params)
		if err != nil || !l {
			return false, err
		}
		return q.eval(e.Right, b, params)
	case Or:
		l, err := q.eval(e.Left, b, params)
		if err != nil || l {
			return l, err
		}
		return q.eval(e.Right, b, params)
	case Not:
		x, err := q.eval(e.X, b, params)
		return !x, err
	case Cmp:
		l, err := q.operand(e.Left, b, params)
		if err != nil {
			return false, err
		}
		r, err := q.operand(e.Right, b, params)
		if err != nil {
			return false, err
		}
		c := compare(l, r)
		switch e.Op {
		case OpEq:
			return c == 0, nil
		case OpNe:
			return c != 0, nil
		case OpLt:
			return c < 0, nil
		case OpLe:
			return c <= 0, nil
		case OpGt:
			return c > 0, nil
		case OpGe:
			return c >= 0, nil
		}
	}
	return false, fmt.Errorf("query: unsupported expression %T", e)
}

func (q *Query) operand(o Operand, b sqlite.Balance, params map[string]any) (any, error) {
	switch o := o.(type) {
	case FieldOperand:
		return fieldValue(o.Field, b), nil
	case ParamOperand:
		return params[q.Params[o.Index].Name], nil
	case Const:
		switch o.Type.Kind {
		case KindNumber:
			return o.Num, nil
		case KindString:
			return o.Str, nil
		}
		return o.Bool, nil
	}
	return nil, fmt.Errorf("query: unsupported operand %T", o)
}

// compare сравнивает два значения одного типа: числа — точно, строки и даты — по байтам, ложь раньше истины,
// пустая ссылка раньше любой, ссылки по номеру записи. Типы согласует проверка запроса.
func compare(a, b any) int {
	switch x := a.(type) {
	case value.Number:
		return x.Cmp(b.(value.Number))
	case string:
		return strings.Compare(x, b.(string))
	case bool:
		y := b.(bool)
		switch {
		case x == y:
			return 0
		case !x:
			return -1
		}
		return 1
	case nil:
		if b == nil {
			return 0
		}
		return -1
	case value.Ref:
		y, ok := b.(value.Ref)
		switch {
		case !ok:
			return 1
		case x.ID < y.ID:
			return -1
		case x.ID > y.ID:
			return 1
		}
		return 0
	}
	return 0
}
