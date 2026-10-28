package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// S03 из копии через процесс: проведены поступление и выдача (8,250), копия снята, восстановленная база показывает 8,250
// и прежние документы; отказы не создают файлов и не трогают существующие; роль чтения снимает копию.
func TestBackupRestoreCommands(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatal(errs)
	}
	seedProducts(t, db)
	for _, a := range [][]string{
		{"document", "add", configDir, db, "receipt", "Номер=R-1", "Дата=2026-10-27", "Комментарий=x", "--row", "receipt-items", "Товар=1", "Количество=10.500"},
		{"document", "add", configDir, db, "issue", "Номер=I-1", "Дата=2026-10-28", "Получатель=цех", "--row", "issue-items", "Товар=1", "Количество=2.250"},
		{"document", "post", configDir, db, "receipt", "1"},
		{"document", "post", configDir, db, "issue", "1"},
	} {
		if code, _, errs := cli(t, a...); code != 0 {
			t.Fatalf("%v: %q", a[:2], errs)
		}
	}
	bak := filepath.Join(dir, "backup.sqlite")
	t.Setenv(RoleEnv, "read")
	if code, out, errs := cli(t, "db", "backup", configDir, db, bak); code != 0 || !strings.Contains(out, "backup written") {
		t.Fatalf("backup under the read role: %d %q %q", code, out, errs)
	}
	t.Setenv(RoleEnv, "")
	if code, _, errs := cli(t, "db", "backup", configDir, db, bak); code != 1 || !strings.Contains(errs, "already exists") {
		t.Errorf("backup over a file: %d %q", code, errs)
	}
	newDB := filepath.Join(dir, "restored.sqlite")
	if code, out, errs := cli(t, "db", "restore", configDir, bak, newDB); code != 0 || !strings.Contains(out, "restored") {
		t.Fatalf("restore: %d %q %q", code, out, errs)
	}
	for _, p := range []string{db, newDB} {
		if _, out, _ := cli(t, "register", "balance", configDir, p, "Запасы"); out != "Товар=1 Количество=8.250\n" {
			t.Errorf("balance of %s = %q", filepath.Base(p), out)
		}
	}
	_, a, _ := cli(t, "document", "list", configDir, db, "receipt")
	_, b, _ := cli(t, "document", "list", configDir, newDB, "receipt")
	if a == "" || a != b {
		t.Errorf("documents differ:\n%q\n%q", a, b)
	}
	// отказы
	if code, _, errs := cli(t, "db", "restore", configDir, bak, newDB); code != 1 || !strings.Contains(errs, "already exists") {
		t.Errorf("restore over a database: %d %q", code, errs)
	}
	bad := filepath.Join(dir, "bad.sqlite")
	os.WriteFile(bad, []byte(strings.Repeat("мусор", 300)), 0o600)
	out := filepath.Join(dir, "never.sqlite")
	if code, _, errs := cli(t, "db", "restore", configDir, bad, out); code != 1 || errs == "" {
		t.Errorf("restore of garbage: %d %q", code, errs)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a file was created from a corrupt backup")
	}
	for _, args := range [][]string{{"db", "backup", configDir, db}, {"db", "restore", configDir, bak}, {"db", "restore"}} {
		if code, _, errs := cli(t, args...); code != 2 || !strings.Contains(errs, "usage:") {
			t.Errorf("%v: %d %q", args, code, errs)
		}
	}
}
