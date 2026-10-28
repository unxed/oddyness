package sqlite

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
)

// S03 из копии: поступление 10,500 и выдача 2,250 проведены (движения и статус), копия снята с открытой базы,
// исходная база закрыта, восстановленная база показывает 8,250 и прежние документы.
func TestBackupRestoreS03(t *testing.T) {
	m := loadModel(t)
	s, _ := openTemp(t)
	seedGoods(t, s, 1)
	rid, err := s.InsertDocument(ctx, "receipt", receiptHeader("R-1", "2026-10-28"), items(receiptLine(t, 1, "10.500")))
	if err != nil {
		t.Fatal(err)
	}
	iid, err := s.InsertDocument(ctx, "issue", map[string]any{"issue-number": "I-1", "issue-date": "2026-10-28", "issue-recipient": "цех"},
		map[string][]map[string]any{"issue-items": {{"issue-product": Ref{Catalog: "goods", ID: 1}, "issue-qty": num(t, "2.250")}}})
	if err != nil {
		t.Fatal(err)
	}
	mv := func(q string) []map[string]any {
		return []map[string]any{{"stock-product": Ref{Catalog: "goods", ID: 1}, "stock-qty": num(t, q)}}
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.ReplaceMovements("stock", "receipt", rid, mv("10.500")); err != nil {
			return err
		}
		if err := tx.ReplaceMovements("stock", "issue", iid, mv("-2.250")); err != nil {
			return err
		}
		if err := tx.SetPosted("receipt", rid, true); err != nil {
			return err
		}
		return tx.SetPosted("issue", iid, true)
	}); err != nil {
		t.Fatal(err)
	}
	// копия снимается с открытой базы (в том числе под ролью чтения), существующий файл не перезаписывается
	dir := t.TempDir()
	bak := filepath.Join(dir, "backup.sqlite")
	s.SetRole(RoleReader)
	if err := s.Backup(ctx, bak); err != nil {
		t.Fatal(err)
	}
	s.SetRole(RoleOperator)
	if err := s.Backup(ctx, bak); !errors.Is(err, ErrExists) {
		t.Errorf("backup over an existing file: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	newDB := filepath.Join(dir, "restored.sqlite")
	if err := Restore(ctx, bak, newDB, m); err != nil {
		t.Fatal(err)
	}
	r, err := Open(newDB, m)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	bs, err := r.Balances(ctx, "stock", BalanceQuery{})
	if err != nil || len(bs) != 1 || bs[0].Resources["stock-qty"].String() != "8.250" {
		t.Fatalf("balances = %v, %v", bs, err)
	}
	for kind, id := range map[string]int64{"receipt": rid, "issue": iid} {
		d, err := r.GetDocument(ctx, kind, id)
		if err != nil || !d.Posted {
			t.Errorf("%s after the restore: %+v %v", kind, d, err)
		}
	}
	// восстановленная база рабочая и независима от копии
	if _, err := r.Insert(ctx, "goods", goods(t, "Z", "Н", "0")); err != nil {
		t.Errorf("the restored database is not writable: %v", err)
	}
}

func TestRestoreRefusals(t *testing.T) {
	m := loadModel(t)
	s, _ := openTemp(t)
	seedGoods(t, s, 1)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.sqlite")
	if err := s.Backup(ctx, good); err != nil {
		t.Fatal(err)
	}
	s.Close()
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	absent := func(name string, p string) {
		t.Helper()
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s: a file was left at %s", name, p)
		}
	}

	// существующая база не перезаписывается, копия не меняется
	existing := filepath.Join(dir, "existing.sqlite")
	if err := os.WriteFile(existing, []byte("важные данные"), 0o600); err != nil {
		t.Fatal(err)
	}
	goodBytes := read(good)
	if err := Restore(ctx, good, existing, m); !errors.Is(err, ErrExists) || string(read(existing)) != "важные данные" || !bytes.Equal(read(good), goodBytes) {
		t.Errorf("restore over an existing file: %v", err)
	}

	// повреждённая копия и не база вовсе
	garbage := filepath.Join(dir, "garbage.sqlite")
	os.WriteFile(garbage, []byte(strings.Repeat("не база данных ", 500)), 0o600)
	truncated := filepath.Join(dir, "truncated.sqlite")
	os.WriteFile(truncated, goodBytes[:len(goodBytes)/3], 0o600)
	for name, p := range map[string]string{"garbage": garbage, "truncated": truncated, "absent source": filepath.Join(dir, "nope.sqlite")} {
		out := filepath.Join(dir, "out-"+strings.ReplaceAll(name, " ", "-")+".sqlite")
		if err := Restore(ctx, p, out, m); err == nil {
			t.Errorf("%s: restore succeeded", name)
		}
		absent(name, out)
	}

	// копия другой конфигурации
	other := *m
	other.Catalogs = append([]metadata.Catalog(nil), m.Catalogs...)
	other.Catalogs[0].Fields = append([]metadata.Field(nil), other.Catalogs[0].Fields[1:]...) // удалено поле
	out := filepath.Join(dir, "other.sqlite")
	if err := Restore(ctx, good, out, &other); !errors.Is(err, ErrConfig) {
		t.Errorf("other configuration: %v", err)
	}
	absent("other configuration", out)

	// другая версия схемы
	old := filepath.Join(dir, "old.sqlite")
	if err := os.WriteFile(old, goodBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out = filepath.Join(dir, "old-out.sqlite")
	if err := Restore(ctx, old, out, m); !errors.Is(err, ErrVersion) {
		t.Errorf("other schema version: %v", err)
	}
	absent("other schema version", out)

	// пустой путь копии и путь с недопустимым символом
	if err := Restore(ctx, good, filepath.Join(dir, "a?b.sqlite"), m); err == nil {
		t.Error("restore to a path with '?' succeeded")
	}
}
