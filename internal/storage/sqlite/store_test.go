package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

func loadModel(t testing.TB) *metadata.Model {
	t.Helper()
	m, err := metadata.Load("../../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func num(t testing.TB, s string) value.Number {
	t.Helper()
	n, err := value.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func goods(t testing.TB, code, title string, min string) map[string]any {
	return map[string]any{
		"goods-code":   code,
		"goods-title":  title,
		"goods-unit":   "м",
		"goods-active": true,
		"goods-min":    num(t, min),
	}
}

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Create(path, loadModel(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestInsertGetAndRestart(t *testing.T) {
	m := loadModel(t)
	s, path := openTemp(t)
	in := goods(t, "K-1", "Кабель", "10.500")
	id, err := s.Insert(ctx, "goods", in)
	if err != nil || id == 0 {
		t.Fatalf("Insert = %d, %v", id, err)
	}
	rec, err := s.Get(ctx, "goods", id)
	if err != nil {
		t.Fatal(err)
	}
	check := func(rec Record) {
		t.Helper()
		if rec.ID != id || rec.Values["goods-code"] != "K-1" || rec.Values["goods-title"] != "Кабель" ||
			rec.Values["goods-unit"] != "м" || rec.Values["goods-active"] != true || rec.Values["goods-analog"] != nil {
			t.Errorf("record = %+v", rec)
		}
		n, ok := rec.Values["goods-min"].(value.Number)
		if !ok || n.String() != "10.500" || n.Scale() != 3 {
			t.Errorf("goods-min = %v (%T), want exactly 10.500", rec.Values["goods-min"], rec.Values["goods-min"])
		}
	}
	check(rec)

	// перезапуск: новое открытие того же файла читает те же данные
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, m)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	list, err := s2.List(ctx, "goods")
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	check(list[0])
	// новая запись получает следующий id
	id2, err := s2.Insert(ctx, "goods", goods(t, "K-2", "Шнур", "0"))
	if err != nil || id2 <= id {
		t.Errorf("second Insert = %d, %v", id2, err)
	}
}

func TestNumbersAreExactAndNeverRounded(t *testing.T) {
	s, _ := openTemp(t)
	for i, lit := range []string{"999999999999.999", "-999999999999.999", "0.001", "0", "12345.670", "0.1"} {
		id, err := s.Insert(ctx, "goods", goods(t, "N"+lit, "т", lit))
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		rec, err := s.Get(ctx, "goods", id)
		if err != nil {
			t.Fatal(err)
		}
		if got := rec.Values["goods-min"].(value.Number).String(); got != lit {
			t.Errorf("case %d: stored %s, read %s", i, lit, got)
		}
	}
	// 0.1 + 0.2 после сохранения и чтения даёт точно 0.3
	a, _ := s.Insert(ctx, "goods", goods(t, "A", "а", "0.1"))
	b, _ := s.Insert(ctx, "goods", goods(t, "B", "б", "0.2"))
	ra, _ := s.Get(ctx, "goods", a)
	rb, _ := s.Get(ctx, "goods", b)
	sum, err := ra.Values["goods-min"].(value.Number).Add(rb.Values["goods-min"].(value.Number))
	if err != nil || sum.String() != "0.3" {
		t.Errorf("0.1 + 0.2 = %v, %v", sum, err)
	}
	before, _ := s.List(ctx, "goods")
	for _, lit := range []string{"0.0001", "1000000000000", "1000000000000.000", "-1000000000000"} {
		if _, err := s.Insert(ctx, "goods", goods(t, "X"+lit, "т", lit)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", lit, err)
		}
	}
	if after, _ := s.List(ctx, "goods"); len(after) != len(before) {
		t.Error("rejected values left records behind")
	}
}

func TestUniqueCode(t *testing.T) {
	s, _ := openTemp(t)
	id1, _ := s.Insert(ctx, "goods", goods(t, "K-1", "Кабель", "1"))
	id2, err := s.Insert(ctx, "goods", goods(t, "K-2", "Шнур", "2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(ctx, "goods", goods(t, "K-1", "Дубль", "3")); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate Insert error = %v", err)
	}
	if list, _ := s.List(ctx, "goods"); len(list) != 2 {
		t.Errorf("duplicate left %d records", len(list))
	}
	if err := s.Update(ctx, "goods", id2, goods(t, "K-1", "Шнур", "2")); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate Update error = %v", err)
	}
	// своё значение при обновлении не конфликтует
	if err := s.Update(ctx, "goods", id1, goods(t, "K-1", "Кабель медный", "1.5")); err != nil {
		t.Errorf("Update keeping own code: %v", err)
	}
	rec, _ := s.Get(ctx, "goods", id1)
	if rec.Values["goods-title"] != "Кабель медный" || rec.Values["goods-min"].(value.Number).String() != "1.5" {
		t.Errorf("after Update: %+v", rec)
	}
	// после смены кода прежний освобождается
	if err := s.Update(ctx, "goods", id1, goods(t, "K-9", "Кабель", "1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(ctx, "goods", goods(t, "K-1", "Новый", "1")); err != nil {
		t.Errorf("reusing freed code: %v", err)
	}
}

func TestInvalidInput(t *testing.T) {
	s, _ := openTemp(t)
	good := func() map[string]any { return goods(t, "K-1", "Кабель", "1") }
	mutate := func(f func(m map[string]any)) map[string]any { m := good(); f(m); return m }
	cases := map[string]map[string]any{
		"unknown field": mutate(func(m map[string]any) { m["nope"] = "x" }),
		"missing field": mutate(func(m map[string]any) { delete(m, "goods-title") }),
		"string type":   mutate(func(m map[string]any) { m["goods-title"] = 5 }),
		"bool type":     mutate(func(m map[string]any) { m["goods-active"] = "yes" }),
		"number type":   mutate(func(m map[string]any) { m["goods-min"] = 1.5 }),
		"ref type":      mutate(func(m map[string]any) { m["goods-analog"] = int64(1) }),
		"ref catalog":   mutate(func(m map[string]any) { m["goods-analog"] = Ref{Catalog: "other", ID: 1} }),
		"ref missing":   mutate(func(m map[string]any) { m["goods-analog"] = Ref{Catalog: "goods", ID: 999} }),
	}
	for name, in := range cases {
		if _, err := s.Insert(ctx, "goods", in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
	if list, _ := s.List(ctx, "goods"); len(list) != 0 {
		t.Errorf("invalid inserts left %d records", len(list))
	}
	if _, err := s.Insert(ctx, "nope", good()); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown catalog Insert: %v", err)
	}
	if _, err := s.Get(ctx, "nope", 1); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown catalog Get: %v", err)
	}
	if _, err := s.List(ctx, "nope"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown catalog List: %v", err)
	}
	if _, err := s.Get(ctx, "goods", 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id Get: %v", err)
	}
	if err := s.Update(ctx, "goods", 999, good()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id Update: %v", err)
	}
	if err := s.Delete(ctx, "goods", 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id Delete: %v", err)
	}
}

func TestReferences(t *testing.T) {
	s, _ := openTemp(t)
	a, _ := s.Insert(ctx, "goods", goods(t, "A", "а", "1"))
	in := goods(t, "B", "б", "1")
	in["goods-analog"] = Ref{Catalog: "goods", ID: a}
	b, err := s.Insert(ctx, "goods", in)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := s.Get(ctx, "goods", b)
	if rec.Values["goods-analog"] != (Ref{Catalog: "goods", ID: a}) {
		t.Errorf("ref = %v", rec.Values["goods-analog"])
	}
	// используемую запись удалить нельзя, она остаётся
	if err := s.Delete(ctx, "goods", a); !errors.Is(err, ErrInvalid) {
		t.Errorf("Delete of a referenced record: %v", err)
	}
	if _, err := s.Get(ctx, "goods", a); err != nil {
		t.Errorf("referenced record vanished: %v", err)
	}
	if err := s.Delete(ctx, "goods", b); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "goods", a); err != nil {
		t.Errorf("Delete after the reference is gone: %v", err)
	}
}

func TestValuesRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	in := goods(t, "x'; DROP TABLE \"cat_goods\"; --", "Кабель «медный» \"2×1,5\"\nвторая строка", "7")
	in["goods-active"] = false
	id, err := s.Insert(ctx, "goods", in)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Get(ctx, "goods", id)
	if err != nil || rec.Values["goods-code"] != in["goods-code"] || rec.Values["goods-title"] != in["goods-title"] || rec.Values["goods-active"] != false {
		t.Errorf("record = %+v, %v", rec, err)
	}
}

func TestSecondSessionIsRefused(t *testing.T) {
	m := loadModel(t)
	s, path := openTemp(t)
	if _, err := s.Insert(ctx, "goods", goods(t, "K-1", "Кабель", "1")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, m); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open error = %v, want ErrLocked", err)
	}
	// первая сессия продолжает работать, данные целы
	if list, err := s.List(ctx, "goods"); err != nil || len(list) != 1 {
		t.Errorf("first session after refused second: %v, %v", list, err)
	}
	if _, err := Create(path, m); !errors.Is(err, ErrExists) {
		t.Errorf("Create over an open database: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, m)
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	s2.Close()
	s3, err := Open(path, m)
	if err != nil {
		t.Fatalf("Open after the second session ended: %v", err)
	}
	s3.Close() // на Windows открытый файл не удаляется вместе с каталогом теста
}

func TestOpenRefusals(t *testing.T) {
	m := loadModel(t)
	s, path := openTemp(t)
	s.Close()

	other := loadModel(t)
	other.Catalogs[0].Fields[1].Type = metadata.TypeBoolean // другая структура
	if _, err := Open(path, other); !errors.Is(err, ErrConfig) {
		t.Errorf("different configuration: %v", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "absent.sqlite"), m); err == nil {
		t.Error("Open of a missing file succeeded")
	}

	raw := func(q string) {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	raw("PRAGMA user_version = 99")
	if _, err := Open(path, m); !errors.Is(err, ErrVersion) {
		t.Errorf("other schema version: %v", err)
	}
	// отказ ничего не менял
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 99 {
		t.Errorf("user_version after refused Open = %d, %v", v, err)
	}

	// чужая база с подходящей версией, но без таблицы метаданных
	foreign := filepath.Join(t.TempDir(), "foreign.sqlite")
	fdb, err := sql.Open("sqlite", foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec("CREATE TABLE t (x); PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	fdb.Close()
	if _, err := Open(foreign, m); err == nil || !strings.Contains(err.Error(), "not an oddyness database") {
		t.Errorf("foreign database: %v", err)
	}
}

func TestCreateRefusals(t *testing.T) {
	m := loadModel(t)
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.sqlite")
	if err := os.WriteFile(existing, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(existing, m); !errors.Is(err, ErrExists) {
		t.Errorf("Create over a file: %v", err)
	}
	if data, _ := os.ReadFile(existing); string(data) != "precious" {
		t.Errorf("existing file was modified: %q", data)
	}
	missing := filepath.Join(dir, "no", "such", "dir", "db.sqlite")
	if _, err := Create(missing, m); err == nil {
		t.Error("Create in a missing directory succeeded")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("failed Create left a file")
	}
	if _, err := Create(filepath.Join(dir, "a?b.sqlite"), m); err == nil {
		t.Error("path with '?' accepted")
	}
}
