package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

const smallStock = "../../testdata/configs/small-stock"

type env struct {
	t     *testing.T
	store *sqlite.Store
	model *metadata.Model
	path  string
	dir   string
}

// newEnv копирует конфигурацию во временный каталог и создаёт базу с тремя товарами.
func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(smallStock)); err != nil {
		t.Fatal(err)
	}
	model, err := metadata.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "db.sqlite")
	store, err := sqlite.Create(path, model)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e := &env{t: t, store: store, model: model, path: path, dir: dir}
	for _, code := range []string{"A", "B", "C"} {
		_, err := store.Insert(ctx, "goods", map[string]any{
			"goods-code": code, "goods-title": "товар " + code, "goods-unit": "шт",
			"goods-active": true, "goods-min": e.num("0"),
		})
		if err != nil {
			t.Fatal(err)
		}
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

func (e *env) receipt(number string, lines ...[2]any) int64 {
	e.t.Helper()
	var rows []map[string]any
	for _, l := range lines {
		rows = append(rows, map[string]any{
			"receipt-product": sqlite.Ref{Catalog: "goods", ID: int64(l[0].(int))},
			"receipt-qty":     e.num(l[1].(string)),
		})
	}
	id, err := e.store.InsertDocument(ctx, "receipt",
		map[string]any{"receipt-number": number, "receipt-date": "2026-10-27", "receipt-comment": ""},
		map[string][]map[string]any{"receipt-items": rows})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) platform(opt Options) *Platform {
	e.t.Helper()
	p, err := Open(e.store, e.dir, opt)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

// withHandler возвращает платформу с другим текстом обработчика (в отдельном каталоге).
func (e *env) withHandler(src string, opt Options) (*Platform, error) {
	e.t.Helper()
	dir := e.t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(smallStock)); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "bsl", "receipt-posting.bsl"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bsl", "receipt-posting.bsl"), []byte(src), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return Open(e.store, dir, opt)
}

func (e *env) balances() map[int64]string {
	e.t.Helper()
	bs, err := e.store.Balances(ctx, "stock", sqlite.BalanceQuery{})
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[int64]string{}
	for _, b := range bs {
		out[b.Dimensions["stock-product"].(sqlite.Ref).ID] = b.Resources["stock-qty"].String()
	}
	return out
}

func (e *env) movementCount(id int64) int {
	e.t.Helper()
	rows, err := e.store.Movements(ctx, "stock", "receipt", id)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(rows)
}

func (e *env) posted(id int64) bool {
	e.t.Helper()
	d, err := e.store.GetDocument(ctx, "receipt", id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d.Posted
}

func eq(t *testing.T, got, want map[int64]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("balances = %v, want %v", got, want)
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("balances = %v, want %v", got, want)
			return
		}
	}
}

// S02: до проведения остаток 0, после — 10,500, статус сохраняется после перезапуска.
// S05: повторное проведение не дублирует движения.
func TestPostS02AndS05(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	id := e.receipt("R-1", [2]any{1, "10.500"})
	eq(t, e.balances(), map[int64]string{})
	if e.posted(id) {
		t.Fatal("a draft is reported as posted")
	}
	if err := pl.Post(ctx, "receipt", id); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.500"})
	if !e.posted(id) || e.movementCount(id) != 1 {
		t.Fatalf("posted=%v movements=%d", e.posted(id), e.movementCount(id))
	}

	// перезапуск: новое открытие базы читает статус и остатки
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(e.path, e.model)
	if err != nil {
		t.Fatal(err)
	}
	e.store = store
	t.Cleanup(func() { store.Close() })
	pl, err = Open(store, e.dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.500"})
	if !e.posted(id) {
		t.Error("status was lost after restart")
	}

	// S05: повторное проведение
	for i := 0; i < 2; i++ {
		if err := pl.Post(ctx, "receipt", id); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, e.balances(), map[int64]string{1: "10.500"})
	if e.movementCount(id) != 1 {
		t.Errorf("movements duplicated: %d", e.movementCount(id))
	}

	// два документа и несколько строк одного товара суммируются точно
	id2 := e.receipt("R-2", [2]any{1, "0.125"}, [2]any{1, "0.1"}, [2]any{2, "2.250"})
	if err := pl.Post(ctx, "receipt", id2); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.725", 2: "2.250"})
}

const brokenHeader = "Процедура ПриПроведении(Документ)\n"

// S08 и отказоустойчивость: ошибка обработчика после подготовки первого движения,
// неверный тип движения, лимит VM, ошибка записи на второй строке — ноль частичных
// данных; повтор после исправления успешен.
func TestPostFailuresLeaveNoPartialData(t *testing.T) {
	e := newEnv(t)
	id := e.receipt("R-1", [2]any{1, "8"}, [2]any{2, "1.5"})
	cases := []struct {
		name string
		src  string
		opt  Options
		msg  string
	}{
		{"error after the first movement", brokenHeader + "\tПерем Х;\n\tДля Каждого Строка Из Документ.Товары Цикл\n\t\tДвижение(\"Запасы\", Строка.Товар, Строка.Количество);\n\t\tХ = 1 / 0;\n\tКонецЦикла;\nКонецПроцедуры\n", Options{}, "division by zero"},
		{"wrong movement type", brokenHeader + "\tДля Каждого Строка Из Документ.Товары Цикл\n\t\tДвижение(\"Запасы\", \"не товар\", Строка.Количество);\n\tКонецЦикла;\nКонецПроцедуры\n", Options{}, "expected a reference"},
		{"quantity is a string", brokenHeader + "\tДля Каждого Строка Из Документ.Товары Цикл\n\t\tДвижение(\"Запасы\", Строка.Товар, \"1\");\n\tКонецЦикла;\nКонецПроцедуры\n", Options{}, "expected Number"},
		{"unknown register", brokenHeader + "\tДвижение(\"Нет\", 1, 2);\nКонецПроцедуры\n", Options{}, `unknown register "Нет"`},
		{"wrong number of values", brokenHeader + "\tДля Каждого Строка Из Документ.Товары Цикл\n\t\tДвижение(\"Запасы\", Строка.Товар);\n\tКонецЦикла;\nКонецПроцедуры\n", Options{}, "takes 2 values"},
		{"register is not a string", brokenHeader + "\tДвижение(1, 2, 3);\nКонецПроцедуры\n", Options{}, "must be a String"},
		{"step limit", brokenHeader + "\tПока Истина Цикл\n\tКонецЦикла;\nКонецПроцедуры\n", Options{MaxSteps: 1000}, "step limit 1000 exceeded"},
		{"unknown column", brokenHeader + "\tДвижение(\"Запасы\", Документ.Нет, 1);\nКонецПроцедуры\n", Options{}, "unknown column"},
		// первая строка корректна (1), вторая даёт 0.1875 — четыре знака, запись отклоняется
		{"write fails on the second row", brokenHeader + "\tДля Каждого Строка Из Документ.Товары Цикл\n\t\tДвижение(\"Запасы\", Строка.Товар, Строка.Количество / 8);\n\tКонецЦикла;\nКонецПроцедуры\n", Options{}, "fraction digits"},
	}
	for _, c := range cases {
		pl, err := e.withHandler(c.src, c.opt)
		if err != nil {
			t.Fatalf("%s: handler did not load: %v", c.name, err)
		}
		err = pl.Post(ctx, "receipt", id)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: error = %v, want containing %q", c.name, err, c.msg)
		}
		if e.posted(id) || e.movementCount(id) != 0 || len(e.balances()) != 0 {
			t.Errorf("%s: partial data left: posted=%v movements=%d balances=%v", c.name, e.posted(id), e.movementCount(id), e.balances())
		}
	}
	// повтор после исправления: обработчик из конфигурации
	if err := e.platform(Options{}).Post(ctx, "receipt", id); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "8", 2: "1.5"})

	// неудачное повторное проведение не трогает уже записанное
	pl, err := e.withHandler(cases[0].src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Post(ctx, "receipt", id); err == nil {
		t.Fatal("broken handler succeeded")
	}
	eq(t, e.balances(), map[int64]string{1: "8", 2: "1.5"})
	if !e.posted(id) || e.movementCount(id) != 2 {
		t.Errorf("a failed re-post changed the posted document: posted=%v movements=%d", e.posted(id), e.movementCount(id))
	}
}

func TestHandlerReadsDocument(t *testing.T) {
	e := newEnv(t)
	pl, err := e.withHandler(brokenHeader+`	Если Документ.Номер = "SKIP" Тогда
		Возврат;
	КонецЕсли;
	Если Документ.Дата <> "2026-10-27" Тогда
		Движение("Нет", 0, 0);
	КонецЕсли;
	Для Каждого Строка Из Документ.Товары Цикл
		Движение("Запасы", Строка.Товар, Строка.Количество + 1);
	КонецЦикла;
КонецПроцедуры
`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	skip := e.receipt("SKIP", [2]any{1, "5"})
	other := e.receipt("R-9", [2]any{3, "5.5"})
	for _, id := range []int64{skip, other} {
		if err := pl.Post(ctx, "receipt", id); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, e.balances(), map[int64]string{3: "6.5"})
	if !e.posted(skip) || e.movementCount(skip) != 0 {
		t.Errorf("a document with no movements: posted=%v movements=%d", e.posted(skip), e.movementCount(skip))
	}
}

func TestHandlerLoadErrors(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name, src, msg string
	}{
		{"syntax error", "Процедура ПриПроведении(Документ)\n", "is not closed"},
		{"wrong procedure name", "Процедура Другая(Документ)\nКонецПроцедуры\n", `procedure "ПриПроведении" not found`},
		{"two parameters", "Процедура ПриПроведении(А, Б)\nКонецПроцедуры\n", "exactly one parameter"},
		{"no parameters", "Процедура ПриПроведении()\nКонецПроцедуры\n", "exactly one parameter"},
		{"unknown routine", brokenHeader + "\tДвижение2(1, 2);\nКонецПроцедуры\n", `unknown routine "Движение2"`},
		{"too few arguments", brokenHeader + "\tДвижение(\"Запасы\");\nКонецПроцедуры\n", "takes 2..-1 arguments"},
		{"unsupported construct", brokenHeader + "\tПопытка\nКонецПроцедуры\n", "is not supported"},
		{"module variable", "Перем Х;\n" + brokenHeader + "КонецПроцедуры\n", "module-level variables"},
	}
	for _, c := range cases {
		_, err := e.withHandler(c.src, Options{})
		if err == nil || !strings.Contains(err.Error(), c.msg) || !strings.Contains(err.Error(), "receipt-posting.bsl") {
			t.Errorf("%s: error = %v, want file name and %q", c.name, err, c.msg)
		}
	}
	// файла нет вовсе
	if _, err := Open(e.store, t.TempDir(), Options{}); err == nil || !strings.Contains(err.Error(), "receipt-posting.bsl") {
		t.Errorf("missing handler file: %v", err)
	}
	// позиция ошибки разбора видна
	_, err := e.withHandler(brokenHeader+"\tА = ;\nКонецПроцедуры\n", Options{})
	if err == nil || !strings.Contains(err.Error(), ":2:6:") {
		t.Errorf("parse error position: %v", err)
	}
}

func TestPostChecks(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	if err := pl.Post(ctx, "nope", 1); !errors.Is(err, ErrNoHandler) {
		t.Errorf("unknown document kind: %v", err)
	}
	if err := pl.Post(ctx, "receipt", 99); !errors.Is(err, sqlite.ErrNotFound) {
		t.Errorf("unknown document: %v", err)
	}
}
