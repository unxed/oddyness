package report

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

type env struct {
	t     *testing.T
	model *metadata.Model
	store *sqlite.Store
	pl    *platform.Platform
	rep   *Report
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../testdata/configs/small-stock")); err != nil {
		t.Fatal(err)
	}
	model, err := metadata.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Create(filepath.Join(t.TempDir(), "db.sqlite"), model)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e := &env{t: t, model: model, store: store}
	zero := e.num("0")
	for _, code := range []string{"A", "B", "C"} {
		if _, err := store.Insert(ctx, "goods", map[string]any{"goods-code": code, "goods-title": "т", "goods-unit": "шт", "goods-active": true, "goods-min": zero}); err != nil {
			t.Fatal(err)
		}
	}
	if e.pl, err = platform.Open(store, dir, platform.Options{}); err != nil {
		t.Fatal(err)
	}
	if e.rep, err = Load(model, "stock-report"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) num(s string) value.Number {
	e.t.Helper()
	n, err := value.Parse(s)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// doc записывает документ с одной строкой на товар и проводит его; возвращает номер документа.
func (e *env) post(kind, number string, lines ...[2]string) int64 {
	e.t.Helper()
	var rows []map[string]any
	for _, l := range lines {
		id := int64(l[0][0] - 'A' + 1)
		rows = append(rows, map[string]any{kind + "-product": value.Ref{Catalog: "goods", ID: id}, kind + "-qty": e.num(l[1])})
	}
	other := map[string]string{"receipt": "receipt-comment", "issue": "issue-recipient"}[kind]
	id, err := e.store.InsertDocument(ctx, kind, map[string]any{kind + "-number": number, kind + "-date": "2026-10-28", other: "x"},
		map[string][]map[string]any{kind + "-items": rows})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.pl.Post(ctx, kind, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) run(values map[string]any) [][2]string {
	e.t.Helper()
	res, err := e.rep.Run(ctx, e.store, values)
	if err != nil {
		e.t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Columns, []string{"Товар", "Остаток"}) {
		e.t.Fatalf("columns = %v", res.Columns)
	}
	out := [][2]string{}
	for _, r := range res.Rows {
		out = append(out, [2]string{value.FromInt64(r[0].(value.Ref).ID).String(), r[1].(value.Number).String()})
	}
	return out
}

func ref(id int64) value.Ref { return value.Ref{Catalog: "goods", ID: id} }

// Пустой склад — пустой отчёт; несколько товаров, дробные количества, фильтр; обновление после проведения и отмены;
// S03: 10,500 - 2,250 = 8,250.
func TestReportStockScenarios(t *testing.T) {
	e := newEnv(t)
	if got := e.run(nil); len(got) != 0 {
		t.Fatalf("empty store: %v", got)
	}
	e.post("receipt", "R-1", [2]string{"A", "10.500"}, [2]string{"B", "0.125"})
	want := [][2]string{{"1", "10.500"}, {"2", "0.125"}}
	if got := e.run(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the receipt: %v, want %v", got, want)
	}
	// параметр не задан: пустая ссылка и отсутствие значения равны
	if got := e.run(map[string]any{"Товар": nil}); !reflect.DeepEqual(got, want) {
		t.Errorf("unset product: %v", got)
	}
	issue := e.post("issue", "I-1", [2]string{"A", "2.250"})
	if got := e.run(nil); !reflect.DeepEqual(got, [][2]string{{"1", "8.250"}, {"2", "0.125"}}) {
		t.Fatalf("S03: %v", got)
	}
	// фильтр по товару: имя параметра без учёта регистра
	if got := e.run(map[string]any{"товар": ref(1)}); !reflect.DeepEqual(got, [][2]string{{"1", "8.250"}}) {
		t.Errorf("product 1: %v", got)
	}
	if got := e.run(map[string]any{"Товар": ref(2)}); !reflect.DeepEqual(got, [][2]string{{"2", "0.125"}}) {
		t.Errorf("product 2: %v", got)
	}
	if got := e.run(map[string]any{"Товар": ref(3)}); len(got) != 0 {
		t.Errorf("product without movements: %v", got)
	}
	// отмена выдачи возвращает остаток, повторное проведение снова списывает
	if err := e.pl.Unpost(ctx, "issue", issue); err != nil {
		t.Fatal(err)
	}
	if got := e.run(nil); !reflect.DeepEqual(got, want) {
		t.Errorf("after unposting the issue: %v", got)
	}
	if err := e.pl.Post(ctx, "issue", issue); err != nil {
		t.Fatal(err)
	}
	if got := e.run(map[string]any{"Товар": ref(1)}); !reflect.DeepEqual(got, [][2]string{{"1", "8.250"}}) {
		t.Errorf("after reposting the issue: %v", got)
	}
}

func TestReportRefusals(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		name   string
		values map[string]any
		msg    string
	}{
		{"unknown parameter", map[string]any{"Склад": ref(1)}, "unknown parameter &Склад"},
		{"wrong catalog", map[string]any{"Товар": value.Ref{Catalog: "nope", ID: 1}}, "expected a reference to"},
		{"not a reference", map[string]any{"Товар": "1"}, "expected ref(goods)"},
	} {
		if _, err := e.rep.Run(ctx, e.store, c.values); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v, want %q", c.name, err, c.msg)
		}
	}
	if len(e.rep.Params()) != 1 || e.rep.Params()[0].Name != "Товар" {
		t.Errorf("params = %v", e.rep.Params())
	}
}

func TestReportLoad(t *testing.T) {
	m, err := metadata.Load("../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Load(m, "остатки"); err != nil || r.ID != "stock-report" || r.Name != "Остатки" {
		t.Errorf("by name: %v %+v", err, r)
	}
	if _, err := Load(m, "нет"); err == nil || !strings.Contains(err.Error(), `unknown report "нет"`) {
		t.Errorf("unknown report: %v", err)
	}
	with := func(text string) *metadata.Model {
		c := *m
		c.Reports = []metadata.Report{{ID: "r", Name: "Р", Query: text}}
		return &c
	}
	for _, c := range []struct{ name, query, msg string }{
		{"number parameter", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество > &Мин", "supports only reference parameters"},
		{"syntax error", "ВЫБРАТЬ Товар ИЗ", "report \"r\""},
		{"unknown field", "ВЫБРАТЬ Цвет ИЗ РегистрНакопления.Запасы.Остатки", "Цвет"},
	} {
		if _, err := Load(with(c.query), "r"); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v, want %q", c.name, err, c.msg)
		}
	}
	// параметр под ИЛИ не вырезается: отчёт без значения — ошибка, а не догадка
	r, err := Load(with("ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Товар ИЛИ Количество > 0"), "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, nil, nil); err == nil || !strings.Contains(err.Error(), "under OR or NOT") {
		t.Errorf("optional parameter under OR: %v", err)
	}
}
