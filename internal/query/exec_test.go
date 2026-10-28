package query

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

func num(t *testing.T, s string) value.Number {
	t.Helper()
	n, err := value.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mv(t *testing.T, product int64, qty string) map[string]any {
	return map[string]any{"stock-product": value.Ref{Catalog: "goods", ID: product}, "stock-qty": num(t, qty)}
}

// stockStore: товары A, B, C (1..3); поступления R-1, R-2 и выдача I-1.
// Остатки: A = 10.500 + 0.125 - 2.250 = 8.375, B = 9.99, C = 0 (движения взаимно гасятся).
func stockStore(t *testing.T) *sqlite.Store {
	t.Helper()
	m := model(t)
	s, err := sqlite.Create(filepath.Join(t.TempDir(), "db.sqlite"), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, code := range []string{"A", "B", "C"} {
		if _, err := s.Insert(ctx, "goods", map[string]any{"goods-code": code, "goods-title": "т", "goods-unit": "шт", "goods-active": true, "goods-min": num(t, "0")}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"R-1", "R-2"} {
		if _, err := s.InsertDocument(ctx, "receipt", map[string]any{"receipt-number": n, "receipt-date": "2026-10-27", "receipt-comment": ""}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertDocument(ctx, "issue", map[string]any{"issue-number": "I-1", "issue-date": "2026-10-28", "issue-recipient": "цех"}, nil); err != nil {
		t.Fatal(err)
	}
	set := func(doc string, id int64, rows ...map[string]any) {
		if err := s.ReplaceMovements(ctx, "stock", doc, id, rows); err != nil {
			t.Fatal(err)
		}
	}
	set("receipt", 1, mv(t, 1, "10.500"), mv(t, 2, "9.99"), mv(t, 3, "1"))
	set("receipt", 2, mv(t, 1, "0.125"))
	set("issue", 1, mv(t, 1, "-2.250"), mv(t, 3, "-1"))
	return s
}

func run(t *testing.T, s *sqlite.Store, src string, params map[string]any) *Result {
	t.Helper()
	q := mustParse(t, src)
	r, err := q.Run(ctx, s, params)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return r
}

func ref(id int64) value.Ref { return value.Ref{Catalog: "goods", ID: id} }

func rows(t *testing.T, r *Result) [][2]string {
	t.Helper()
	var out [][2]string
	for _, row := range r.Rows {
		out = append(out, [2]string{
			row[0].(value.Ref).Catalog + "#" + value.FromInt64(row[0].(value.Ref).ID).String(),
			row[1].(value.Number).String(),
		})
	}
	return out
}

// Показать: текстовый запрос даёт тот же остаток, что API P11.
func TestQueryMatchesBalancesAPI(t *testing.T) {
	s := stockStore(t)
	api, err := s.Balances(ctx, "stock", sqlite.BalanceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	r := run(t, s, "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки", nil)
	if len(r.Rows) != len(api) || !reflect.DeepEqual(r.Columns, []string{"Товар", "Количество"}) {
		t.Fatalf("rows = %v, api = %v", r.Rows, api)
	}
	for i, b := range api {
		if r.Rows[i][0] != any(b.Dimensions["stock-product"]) || r.Rows[i][1].(value.Number).Cmp(b.Resources["stock-qty"]) != 0 {
			t.Errorf("row %d = %v, api = %v", i, r.Rows[i], b)
		}
	}
	want := [][2]string{{"goods#1", "8.375"}, {"goods#2", "9.99"}, {"goods#3", "0"}}
	if got := rows(t, r); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func TestWhereParamsAndExactComparison(t *testing.T) {
	s := stockStore(t)
	cases := []struct {
		name, src string
		params    map[string]any
		want      [][2]string
	}{
		{"by product", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Т", map[string]any{"Т": ref(1)}, [][2]string{{"goods#1", "8.375"}}},
		{"not product", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар <> &Т И Количество > 0", map[string]any{"Т": ref(1)}, [][2]string{{"goods#2", "9.99"}}},
		// 8.375 < 9.99 по значению (а не по тексту: "8.375" > "10")
		{"exact less", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество < 9.5 И Количество >= 8.375", nil, [][2]string{{"goods#1", "8.375"}}},
		{"zero equals zero", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество = 0", nil, [][2]string{{"goods#3", "0"}}},
		{"or not", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Т ИЛИ НЕ Количество > 1", map[string]any{"Т": ref(2)}, [][2]string{{"goods#2", "9.99"}, {"goods#3", "0"}}},
		{"param number", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество > &М", map[string]any{"М": num(t, "9.989")}, [][2]string{{"goods#2", "9.99"}}},
		{"empty ref none", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Т", map[string]any{"Т": nil}, nil},
		{"order desc", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки УПОРЯДОЧИТЬ ПО Количество УБЫВ", nil, [][2]string{{"goods#2", "9.99"}, {"goods#1", "8.375"}, {"goods#3", "0"}}},
		{"order asc by value", "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки УПОРЯДОЧИТЬ ПО Количество", nil, [][2]string{{"goods#3", "0"}, {"goods#1", "8.375"}, {"goods#2", "9.99"}}},
		{"order by ref desc alias", "ВЫБРАТЬ Товар КАК Т, Количество ИЗ РегистрНакопления.Запасы.Остатки УПОРЯДОЧИТЬ ПО Т УБЫВ", nil, [][2]string{{"goods#3", "0"}, {"goods#2", "9.99"}, {"goods#1", "8.375"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rows(t, run(t, s, c.src, c.params))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("rows = %v, want %v", got, c.want)
			}
		})
	}
}

func TestColumnSubsetAndAlias(t *testing.T) {
	s := stockStore(t)
	r := run(t, s, "ВЫБРАТЬ Количество КАК Остаток ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Т", map[string]any{"Т": ref(2)})
	if !reflect.DeepEqual(r.Columns, []string{"Остаток"}) || len(r.Rows) != 1 || r.Rows[0][0].(value.Number).String() != "9.99" {
		t.Errorf("result = %+v", r)
	}
}

func TestRunRefusals(t *testing.T) {
	s := stockStore(t)
	q := mustParse(t, "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Т")
	for name, params := range map[string]map[string]any{
		"no param":   nil,
		"wrong type": {"Т": "1"},
		"float":      {"Т": ref(1), "Л": 1.5},
	} {
		if _, err := q.Run(ctx, s, params); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// SQL-метасимволы в строковом параметре — данные: ни ошибки SQL, ни побочного эффекта.
	m := model(t)
	if _, err := Parse(`ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество = 0; DROP TABLE reg_stock`, m); err == nil {
		t.Error("statement list accepted")
	}
	if r := run(t, s, "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки", nil); len(r.Rows) != 3 {
		t.Errorf("rows = %d", len(r.Rows))
	}
}
