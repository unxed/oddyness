package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// S11 через CLI: под ролью чтения ODDYNESS_ROLE=read запись отклоняется ядром (каталог, документ, проведение, отмена),
// чтение и отчёты доступны, база не меняется; роль оператора и пустая переменная возвращают запись.
func TestRoleEnvReadOnly(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}
	seedProducts(t, db)
	rcpt := []string{"document", "add", configDir, db, "receipt", "Номер=R-1", "Дата=2026-10-27", "Комментарий=x", "--row", "receipt-items", "Товар=1", "Количество=10.500"}
	if code, _, errs := cli(t, rcpt...); code != 0 {
		t.Fatal(errs)
	}
	if code, _, errs := cli(t, "document", "post", configDir, db, "receipt", "1"); code != 0 {
		t.Fatal(errs)
	}
	balance := func() string {
		_, out, _ := cli(t, "register", "balance", configDir, db, "Запасы")
		return out
	}
	const want = "Товар=1 Количество=10.500\n"
	if balance() != want {
		t.Fatalf("balance = %q", balance())
	}
	t.Setenv(RoleEnv, "read")
	denied := [][]string{
		{"catalog", "add", configDir, db, "goods", "Код=Z", "Наименование=Н", "ЕдиницаИзмерения=шт", "Используется=true", "МинимальныйОстаток=0"},
		append([]string{}, "document", "add", configDir, db, "issue", "Номер=I-1", "Дата=2026-10-28", "Получатель=цех", "--row", "issue-items", "Товар=1", "Количество=1"),
		{"document", "post", configDir, db, "receipt", "1"},
		{"document", "unpost", configDir, db, "receipt", "1"},
	}
	for _, args := range denied {
		if code, out, errs := cli(t, args...); code != 1 || out != "" || !strings.Contains(errs, "not permitted for the session role") {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q", args[:2], code, out, errs)
		}
	}
	// чтение и отчёты доступны
	for _, args := range [][]string{
		{"catalog", "list", configDir, db, "goods"},
		{"document", "list", configDir, db, "receipt"},
		{"register", "balance", configDir, db, "Запасы"},
		{"query", configDir, db, "ВЫБРАТЬ Товар, Количество ИЗ РегистрНакопления.Запасы.Остатки"},
	} {
		if code, out, errs := cli(t, args...); code != 0 || out == "" {
			t.Errorf("read %v: exit=%d stdout=%q stderr=%q", args[:2], code, out, errs)
		}
	}
	t.Setenv(RoleEnv, "")
	if balance() != want {
		t.Errorf("balance after the refusals = %q", balance())
	}
	if _, out, _ := cli(t, "document", "list", configDir, db, "issue"); out != "" {
		t.Errorf("a document was written under the read role: %q", out)
	}
	// неизвестная роль — ошибка до открытия базы; оператор пишет
	t.Setenv(RoleEnv, "admin")
	if code, _, errs := cli(t, "catalog", "list", configDir, db, "goods"); code != 1 || !strings.Contains(errs, `unknown role "admin"`) {
		t.Errorf("unknown role: exit=%d stderr=%q", code, errs)
	}
	t.Setenv(RoleEnv, "Operator")
	if code, _, errs := cli(t, "document", "unpost", configDir, db, "receipt", "1"); code != 0 {
		t.Errorf("the operator role cannot unpost: %q", errs)
	}
}
