package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// document unpost: остаток возвращается, повтор отмены и отмена с отрицательным остатком — отказ без изменений.
func TestUnpostCommand(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}
	seedProducts(t, db)
	run := func(args ...string) (int, string, string) { return cli(t, args...) }
	if code, _, errs := run("document", "add", configDir, db, "receipt", "Номер=R-1", "Дата=2026-10-27", "Комментарий=x", "--row", "receipt-items", "Товар=1", "Количество=10.500"); code != 0 {
		t.Fatal(errs)
	}
	if code, _, errs := run("document", "add", configDir, db, "issue", "Номер=I-1", "Дата=2026-10-28", "Получатель=цех", "--row", "issue-items", "Товар=1", "Количество=2.250"); code != 0 {
		t.Fatal(errs)
	}
	for _, a := range [][]string{{"receipt", "1"}, {"issue", "1"}} {
		if code, _, errs := run("document", "post", configDir, db, a[0], a[1]); code != 0 {
			t.Fatalf("post %v: %q", a, errs)
		}
	}
	balance := func() string {
		_, out, _ := run("register", "balance", configDir, db, "Запасы")
		return out
	}
	if got := balance(); got != "Товар=1 Количество=8.250\n" {
		t.Fatalf("balance = %q", got)
	}
	if code, out, errs := run("document", "unpost", configDir, db, "receipt", "1"); code != 1 || out != "" || !strings.Contains(errs, "отрицательный остаток") {
		t.Errorf("unpost of the receipt: %d %q %q", code, out, errs)
	}
	if got := balance(); got != "Товар=1 Количество=8.250\n" {
		t.Errorf("balance after a refused cancel = %q", got)
	}
	if code, out, errs := run("document", "unpost", configDir, db, "issue", "1"); code != 0 || out != "unposted 1\n" || errs != "" {
		t.Errorf("unpost of the issue: %d %q %q", code, out, errs)
	}
	if got := balance(); got != "Товар=1 Количество=10.500\n" {
		t.Errorf("balance after cancel = %q", got)
	}
	if code, out, errs := run("document", "unpost", configDir, db, "issue", "1"); code != 1 || out != "" || !strings.Contains(errs, "not posted") {
		t.Errorf("repeated unpost: %d %q %q", code, out, errs)
	}
	if code, _, errs := run("document", "unpost", configDir, db, "issue"); code != 2 || !strings.Contains(errs, "usage:") {
		t.Errorf("usage: %d %q", code, errs)
	}
}
