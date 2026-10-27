package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

// Регистр накопления — низкоуровневый API ядра: им пользуется проведение
// документов, интерфейс его не вызывает. Движения документа заменяются целым
// набором внутри транзакции; остатки считаются в ядре точным сложением
// value.Number по записям движения: SQL-агрегаты и сортировка по числам не
// используются, потому что числа хранятся текстом, а приблизительная арифметика
// запрещена.

func regTable(registerID string) string { return `"reg_` + registerID + `"` }

// regFields — измерения, затем ресурсы: порядок столбцов таблицы движений.
func regFields(r *metadata.Register) []metadata.Field {
	return append(append([]metadata.Field(nil), r.Dimensions...), r.Resources...)
}

func (s *Store) register(id string) (*metadata.Register, error) {
	for i := range s.model.Registers {
		if s.model.Registers[i].ID == id {
			return &s.model.Registers[i], nil
		}
	}
	return nil, fmt.Errorf("%w: register %q", ErrUnknown, id)
}

// Tx — открытая транзакция хранилища; живёт только внутри Store.Tx.
type Tx struct {
	s   *Store
	tx  *sql.Tx
	ctx context.Context
}

// Tx выполняет f в одной транзакции: ошибка f откатывает всё сделанное через Tx.
func (s *Store) Tx(ctx context.Context, f func(*Tx) error) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return f(&Tx{s: s, tx: tx, ctx: ctx}) })
}

// ReplaceMovements заменяет весь набор движений регистратора (вид документа и
// его номер) в регистре: старые удаляются, новые записываются со строками с единицы.
// Ключи строки — ровно измерения и ресурсы регистра. Регистратор обязан существовать.
func (t *Tx) ReplaceMovements(registerID, recorderDoc string, recorderID int64, rows []map[string]any) error {
	r, err := t.s.register(registerID)
	if err != nil {
		return err
	}
	d, err := t.s.document(recorderDoc)
	if err != nil {
		return err
	}
	var one int
	err = t.tx.QueryRowContext(t.ctx, fmt.Sprintf(`SELECT 1 FROM %s WHERE "id" = ?`, docTable(d.ID)), recorderID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: recorder %s %d", ErrNotFound, recorderDoc, recorderID)
	}
	if err != nil {
		return err
	}
	fields := regFields(r)
	encoded := make([][]any, len(rows))
	for i, row := range rows {
		if encoded[i], err = encodeFields(fmt.Sprintf("register %s row %d", r.ID, i+1), fields, row); err != nil {
			return err
		}
	}
	if _, err := t.tx.ExecContext(t.ctx, fmt.Sprintf(`DELETE FROM %s WHERE "recorder_doc" = ? AND "recorder_id" = ?`, regTable(r.ID)), recorderDoc, recorderID); err != nil {
		return mapErr(err)
	}
	cols := []string{`"recorder_doc"`, `"recorder_id"`, `"line"`}
	marks := []string{"?", "?", "?"}
	for _, f := range fields {
		cols = append(cols, columnName(f.ID))
		marks = append(marks, "?")
	}
	q := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, regTable(r.ID), strings.Join(cols, ", "), strings.Join(marks, ", "))
	for i, enc := range encoded {
		if _, err := t.tx.ExecContext(t.ctx, q, append([]any{recorderDoc, recorderID, i + 1}, enc...)...); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// ReplaceMovements — то же в собственной транзакции.
func (s *Store) ReplaceMovements(ctx context.Context, registerID, recorderDoc string, recorderID int64, rows []map[string]any) error {
	return s.Tx(ctx, func(t *Tx) error { return t.ReplaceMovements(registerID, recorderDoc, recorderID, rows) })
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Movements возвращает движения регистратора по номеру строки.
func (s *Store) Movements(ctx context.Context, registerID, recorderDoc string, recorderID int64) ([]Record, error) {
	return movements(ctx, s.db, s, registerID, recorderDoc, recorderID)
}

// Movements возвращает движения регистратора внутри транзакции.
func (t *Tx) Movements(registerID, recorderDoc string, recorderID int64) ([]Record, error) {
	return movements(t.ctx, t.tx, t.s, registerID, recorderDoc, recorderID)
}

func movements(ctx context.Context, q querier, s *Store, registerID, recorderDoc string, recorderID int64) ([]Record, error) {
	r, err := s.register(registerID)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, selectFields(regTable(r.ID), `"line"`, regFields(r))+` WHERE "recorder_doc" = ? AND "recorder_id" = ? ORDER BY "line"`, recorderDoc, recorderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		rec, err := scanFields(regFields(r), rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Balance — остаток: значения измерений и сумма каждого ресурса.
type Balance struct {
	Dimensions map[string]any
	Resources  map[string]value.Number
}

// BalanceQuery уточняет чтение остатков. Filter — равенство по измерениям
// (nil — пустая ссылка); OrderBy — id ресурса, по которому упорядочить результат
// точным числовым сравнением (по умолчанию — по значениям измерений); Nonzero
// отбрасывает остатки, где все ресурсы равны нулю.
type BalanceQuery struct {
	Filter  map[string]any
	OrderBy string
	Desc    bool
	Nonzero bool
}

// Balances суммирует движения по сочетаниям значений измерений.
func (s *Store) Balances(ctx context.Context, registerID string, q BalanceQuery) ([]Balance, error) {
	return balances(ctx, s.db, s, registerID, q)
}

// Balances суммирует движения внутри транзакции, включая ещё не записанные.
func (t *Tx) Balances(registerID string, q BalanceQuery) ([]Balance, error) {
	return balances(t.ctx, t.tx, t.s, registerID, q)
}

func balances(ctx context.Context, qr querier, s *Store, registerID string, q BalanceQuery) ([]Balance, error) {
	r, err := s.register(registerID)
	if err != nil {
		return nil, err
	}
	var where []string
	var args []any
	for id, v := range q.Filter {
		var dim *metadata.Field
		for i := range r.Dimensions {
			if r.Dimensions[i].ID == id {
				dim = &r.Dimensions[i]
			}
		}
		if dim == nil {
			return nil, invalid("filter field %q is not a dimension of register %q", id, r.ID)
		}
		if v == nil && dim.Type == metadata.TypeRef {
			where = append(where, columnName(id)+" IS NULL")
			continue
		}
		enc, err := encode(*dim, v)
		if err != nil {
			return nil, err
		}
		where = append(where, columnName(id)+" = ?")
		args = append(args, enc)
	}
	if q.OrderBy != "" {
		found := false
		for _, f := range r.Resources {
			found = found || f.ID == q.OrderBy
		}
		if !found {
			return nil, invalid("order field %q is not a resource of register %q", q.OrderBy, r.ID)
		}
	}
	sqlText := selectFields(regTable(r.ID), `"id"`, regFields(r))
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := qr.QueryContext(ctx, sqlText+` ORDER BY "id"`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]*Balance{}
	var order []string
	for rows.Next() {
		rec, err := scanFields(regFields(r), rows)
		if err != nil {
			return nil, err
		}
		key := dimensionKey(r.Dimensions, rec.Values)
		g := groups[key]
		if g == nil {
			g = &Balance{Dimensions: map[string]any{}, Resources: map[string]value.Number{}}
			for _, f := range r.Dimensions {
				g.Dimensions[f.ID] = rec.Values[f.ID]
			}
			for _, f := range r.Resources {
				g.Resources[f.ID] = value.FromInt64(0)
			}
			groups[key] = g
			order = append(order, key)
		}
		for _, f := range r.Resources {
			sum, err := g.Resources[f.ID].Add(rec.Values[f.ID].(value.Number))
			if err != nil {
				return nil, fmt.Errorf("sqlite: register %q: balance of %q overflows: %w", r.ID, f.ID, err)
			}
			g.Resources[f.ID] = sum
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Balance, 0, len(order))
	for _, key := range order {
		g := groups[key]
		if q.Nonzero {
			zero := true
			for _, n := range g.Resources {
				zero = zero && n.Sign() == 0
			}
			if zero {
				continue
			}
		}
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if q.OrderBy != "" {
			c := out[i].Resources[q.OrderBy].Cmp(out[j].Resources[q.OrderBy])
			if c != 0 {
				return (c < 0) != q.Desc
			}
		}
		return compareDimensions(r.Dimensions, out[i].Dimensions, out[j].Dimensions) < 0
	})
	return out, nil
}

func dimensionKey(dims []metadata.Field, values map[string]any) string {
	var sb strings.Builder
	for _, f := range dims {
		switch v := values[f.ID].(type) {
		case nil:
			sb.WriteString("n;")
		case Ref:
			fmt.Fprintf(&sb, "r%d;", v.ID)
		case string:
			fmt.Fprintf(&sb, "s%d:%s;", len(v), v)
		case bool:
			fmt.Fprintf(&sb, "b%t;", v)
		}
	}
	return sb.String()
}

// compareDimensions упорядочивает остатки по значениям измерений: пустая ссылка
// первой, ссылки — по номеру записи, строки и даты — по байтам, ложь раньше истины.
func compareDimensions(dims []metadata.Field, a, b map[string]any) int {
	for _, f := range dims {
		if c := compareDim(a[f.ID], b[f.ID]); c != 0 {
			return c
		}
	}
	return 0
}

func compareDim(a, b any) int {
	switch x := a.(type) {
	case nil:
		if b == nil {
			return 0
		}
		return -1
	case Ref:
		y, ok := b.(Ref)
		if !ok {
			return 1
		}
		switch {
		case x.ID < y.ID:
			return -1
		case x.ID > y.ID:
			return 1
		}
		return 0
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
	}
	return 0
}
