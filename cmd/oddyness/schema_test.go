package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editConfig копирует конфигурацию и применяет правку к разобранному metadata.json.
func editConfig(t *testing.T, edit func(m map[string]any)) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(configDir)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// P21 через процесс: переименование подписи сохраняет данные, смена типа поля отклоняется до записи понятным сообщением
// о будущей миграции, прежняя конфигурация по-прежнему открывает базу.
func TestConfigChangeCommands(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatal(errs)
	}
	seedProducts(t, db)
	_, before, _ := cli(t, "catalog", "list", configDir, db, "goods")
	if before == "" {
		t.Fatal("no products")
	}
	goodsFields := func(m map[string]any) []any {
		return m["catalogs"].([]any)[0].(map[string]any)["fields"].([]any)
	}
	renamed := editConfig(t, func(m map[string]any) {
		goodsFields(m)[1].(map[string]any)["name"] = "Название"
	})
	if code, out, errs := cli(t, "catalog", "list", renamed, db, "goods"); code != 0 || out == "" {
		t.Fatalf("renamed label: %d %q %q", code, out, errs)
	}
	retyped := editConfig(t, func(m map[string]any) {
		goodsFields(m)[1].(map[string]any)["type"] = "boolean"
	})
	if code, out, errs := cli(t, "catalog", "list", retyped, db, "goods"); code != 1 || out != "" ||
		!strings.Contains(errs, "migration is required") || !strings.Contains(errs, "goods-title") {
		t.Errorf("changed type: %d %q %q", code, out, errs)
	}
	if code, _, errs := cli(t, "catalog", "add", retyped, db, "goods", "Код=Z"); code != 1 || !strings.Contains(errs, "migration is required") {
		t.Errorf("a write with a changed type: %d %q", code, errs)
	}
	if _, after, _ := cli(t, "catalog", "list", configDir, db, "goods"); after != before {
		t.Errorf("data changed:\n%q\n%q", before, after)
	}
}
