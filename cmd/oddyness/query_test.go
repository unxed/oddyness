package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Один текстовый запрос даёт те же остатки, что `register balance` (API P11).
func TestQueryCommand(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}
	seedProducts(t, db)
	add := []string{"document", "add", configDir, db, "receipt", "Номер=R-1", "Дата=2026-10-27", "Комментарий=x",
		"--row", "receipt-items", "Товар=1", "Количество=10.500", "--row", "receipt-items", "Товар=2", "Количество=2.250", "--row", "receipt-items", "Товар=1", "Количество=0.125"}
	if code, _, errs := cli(t, add...); code != 0 {
		t.Fatalf("add: %d %q", code, errs)
	}
	const all = "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки"
	if code, out, errs := cli(t, "query", configDir, db, all); code != 0 || out != "Товар\tКоличество\n" || errs != "" {
		t.Fatalf("query before posting: %d %q %q", code, out, errs)
	}
	if code, _, errs := cli(t, "document", "post", configDir, db, "receipt", "1"); code != 0 {
		t.Fatalf("post: %d %q", code, errs)
	}
	if code, out, errs := cli(t, "query", configDir, db, all); code != 0 || out != "Товар\tКоличество\n1\t10.625\n2\t2.250\n" || errs != "" {
		t.Errorf("query after posting: %d %q %q", code, out, errs)
	}
	if code, out, _ := cli(t, "query", configDir, db, all+" ГДЕ Товар = &Т", "Т=2"); code != 0 || out != "Товар\tКоличество\n2\t2.250\n" {
		t.Errorf("query by product: %d %q", code, out)
	}
	if code, out, _ := cli(t, "query", configDir, db, all+" ГДЕ Количество > &М УПОРЯДОЧИТЬ ПО Количество УБЫВ", "М=2.2501"); code != 0 || out != "Товар\tКоличество\n1\t10.625\n" {
		t.Errorf("query by quantity: %d %q", code, out)
	}
	for name, c := range map[string]struct {
		args []string
		code int
		msg  string
	}{
		"usage":         {[]string{"query", configDir, db}, 2, "usage:"},
		"unknown field": {[]string{"query", configDir, db, "ВЫБРАТЬ Цвет ИЗ РегистрНакопления.Запасы.Остатки"}, 1, `1:9: unknown field "Цвет"`},
		"unsupported":   {[]string{"query", configDir, db, "ВЫБРАТЬ * ИЗ РегистрНакопления.Запасы.Остатки"}, 1, "not supported"},
		"missing param": {[]string{"query", configDir, db, all + " ГДЕ Товар = &Т"}, 1, "&Т is not set"},
		"bad number":    {[]string{"query", configDir, db, all + " ГДЕ Количество > &М", "М=abc"}, 1, "parameter &М"},
		"bad ref":       {[]string{"query", configDir, db, all + " ГДЕ Товар = &Т", "Т=abc"}, 1, "parameter &Т"},
		"extra param":   {[]string{"query", configDir, db, all, "Т=1"}, 1, "unknown parameter &Т"},
		"no equals":     {[]string{"query", configDir, db, all, "Т"}, 1, "expected name=value"},
		"sql text":      {[]string{"query", configDir, db, all + "; DROP TABLE reg_stock"}, 1, "unexpected"},
	} {
		code, out, errs := cli(t, c.args...)
		if code != c.code || out != "" || !strings.Contains(errs, c.msg) {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q; want exit %d containing %q", name, code, out, errs, c.code, c.msg)
		}
	}
	if _, out, _ := cli(t, "query", configDir, db, all); !strings.HasSuffix(out, "2\t2.250\n") {
		t.Errorf("data changed after refused queries: %q", out)
	}
}
