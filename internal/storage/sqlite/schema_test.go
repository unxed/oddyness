package sqlite

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
)

// Допустимые изменения конфигурации не трогают схему: подписи, признаки проверки ввода, обработчики и отчёты.
// База открывается новой конфигурацией, данные на месте.
func TestSchemaAllowsLabelChanges(t *testing.T) {
	s, path := openTemp(t)
	id, err := s.Insert(ctx, "goods", goods(t, "K-1", "Кабель", "10.500"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	m := loadModel(t)
	m.Catalogs[0].Name = "Номенклатура"
	m.Catalogs[0].Fields[1].Name = "Название"
	m.Documents[0].Name = "Приход"
	m.Documents[0].Tables[0].Fields[1].Positive = false
	m.Documents[0].Tables[0].Fields[0].Required = false
	m.Documents[0].Posting.File = "bsl/other.bsl"
	m.Registers[0].Resources[0].Name = "Объём"
	m.Reports = append(m.Reports, metadata.Report{ID: "extra", Name: "Ещё", Query: "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки"})
	r, err := Open(path, m)
	if err != nil {
		t.Fatalf("a label change was refused: %v", err)
	}
	defer r.Close()
	rec, err := r.Get(ctx, "goods", id)
	if err != nil || rec.Values["goods-title"] != "Кабель" || rec.Values["goods-min"].(interface{ String() string }).String() != "10.500" {
		t.Errorf("data after the label change: %+v %v", rec, err)
	}
}

// Структурные изменения отклоняются при открытии, до любой записи, с перечнем различий; прежняя конфигурация по-прежнему
// открывает базу, данные целы.
func TestSchemaRefusesStructuralChanges(t *testing.T) {
	s, path := openTemp(t)
	id, err := s.Insert(ctx, "goods", goods(t, "K-1", "Кабель", "10.500"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	cases := []struct {
		name   string
		change func(m *metadata.Model)
		want   []string
	}{
		{"type", func(m *metadata.Model) { m.Catalogs[0].Fields[1].Type = metadata.TypeBoolean }, []string{"- catalog goods field goods-title type=string", "+ catalog goods field goods-title type=boolean"}},
		{"removed field", func(m *metadata.Model) { m.Catalogs[0].Fields = m.Catalogs[0].Fields[:5] }, []string{"- catalog goods field goods-analog"}},
		{"added field", func(m *metadata.Model) {
			m.Catalogs[0].Fields = append(m.Catalogs[0].Fields, metadata.Field{ID: "goods-note", Name: "Заметка", Type: metadata.TypeString})
		}, []string{"+ catalog goods field goods-note"}},
		{"renamed id", func(m *metadata.Model) { m.Catalogs[0].Fields[0].ID = "goods-sku" }, []string{"- catalog goods field goods-code", "+ catalog goods field goods-sku"}},
		{"precision", func(m *metadata.Model) { m.Catalogs[0].Fields[4].Scale = 2 }, []string{"scale=3", "scale=2"}},
		{"unique", func(m *metadata.Model) { m.Catalogs[0].Fields[1].Unique = true }, []string{"unique=false", "unique=true"}},
		{"reference target", func(m *metadata.Model) { m.Documents[0].Tables[0].Fields[0].Target = "other" }, []string{"target=goods", "target=other"}},
		{"new document", func(m *metadata.Model) {
			m.Documents = append(m.Documents, metadata.Document{ID: "extra", Name: "Ещё", Fields: []metadata.Field{{ID: "x", Name: "Х", Type: metadata.TypeString}}})
		}, []string{"+ document extra"}},
		{"removed register", func(m *metadata.Model) { m.Registers = nil }, []string{"- register stock"}},
	}
	for _, c := range cases {
		m := loadModel(t)
		c.change(m)
		_, err := Open(path, m)
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "migration is required") {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: the message has no %q:\n%v", c.name, w, err)
			}
		}
	}
	r, err := Open(path, loadModel(t))
	if err != nil {
		t.Fatalf("the original configuration no longer opens the database: %v", err)
	}
	defer r.Close()
	if rec, err := r.Get(ctx, "goods", id); err != nil || rec.Values["goods-code"] != "K-1" {
		t.Errorf("data after the refusals: %+v %v", rec, err)
	}
}

// База, созданная до сохранения схемы (в ней только отпечаток всей модели), открывается неизменённой конфигурацией и
// отклоняет любое её изменение.
func TestSchemaLegacyDatabase(t *testing.T) {
	s, path := openTemp(t)
	s.Close()
	legacy, err := fingerprint(loadModel(t))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM oddyness_meta WHERE key = 'schema'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE oddyness_meta SET value = ? WHERE key = 'fingerprint'`, legacy); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := Open(path, loadModel(t))
	if err != nil {
		t.Fatalf("a legacy database does not open with the same configuration: %v", err)
	}
	r.Close()
	m := loadModel(t)
	m.Catalogs[0].Name = "Другое"
	if _, err := Open(path, m); !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "no stored schema") {
		t.Errorf("a legacy database with a changed configuration: %v", err)
	}
}
