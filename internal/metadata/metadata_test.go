package metadata

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const smallStock = "../../testdata/configs/small-stock"

func TestLoadSmallStock(t *testing.T) {
	m, err := Load(smallStock)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || len(m.Catalogs) != 1 {
		t.Fatalf("model = %+v", m)
	}
	if len(m.Registers) != 1 || m.Registers[0].ID != "stock" || len(m.Registers[0].Dimensions) != 1 || len(m.Registers[0].Resources) != 1 {
		t.Errorf("registers = %+v", m.Registers)
	}
	if len(m.Documents) != 2 || m.Documents[0].ID != "receipt" || m.Documents[1].ID != "issue" {
		t.Errorf("documents = %+v", m.Documents)
	}
	if r := m.Documents[0]; len(r.Fields) != 3 || !r.Fields[0].Unique || r.Fields[1].Type != TypeDate || len(r.Tables) != 1 ||
		!r.Tables[0].Fields[0].Required || !r.Tables[0].Fields[1].Positive {
		t.Errorf("receipt = %+v", r)
	}
	c := m.Catalogs[0]
	if !c.Fields[0].Unique || c.Fields[1].Unique {
		t.Errorf("unique flags: %+v", c.Fields)
	}
	if c.ID != "goods" || c.Name != "Товары" || len(c.Fields) != 6 {
		t.Fatalf("catalog = %+v", c)
	}
	var got []string
	for _, f := range c.Fields {
		got = append(got, f.ID+" "+f.Name+" "+f.TypeString())
	}
	want := []string{
		"goods-code Код string",
		"goods-title Наименование string",
		"goods-unit ЕдиницаИзмерения string",
		"goods-active Используется boolean",
		"goods-min МинимальныйОстаток number(15,3)",
		"goods-analog Аналог ref(goods)",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("fields = %q, want %q", got, want)
	}
}

func cfg(fields string) string {
	return `{"version":1,"catalogs":[{"id":"c1","name":"Товары","fields":[` + fields + `]}]}`
}

const okField = `{"id":"f1","name":"Код","type":"string"}`

func TestDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		path string
		msg  string
		line int // 0 — позиции нет
		col  int
	}{
		{"empty", "", "", "file is empty", 0, 0},
		{"blank", " \n\t ", "", "file is empty", 0, 0},
		{"truncated", `{"version": 1,`, "", "invalid JSON", 1, 15},
		{"bad token", "{\n  \"version\": 1,\n  \"catalogs\": [,]\n}", "", "invalid JSON", 3, 16},
		{"bom", "\xef\xbb\xbf" + cfg(okField), "", "invalid JSON", 1, 1},
		{"not an object", `[]`, "", "wrong type", 1, 0},
		{"missing version", `{"catalogs":[]}`, "version", "required field is missing", 0, 0},
		{"future version", "{\n \"version\": 2,\n \"widgets\": 1\n}", "version", "unsupported version 2", 2, 2},
		{"version type", `{"version":"1"}`, "version", "wrong type", 1, 0},
		{"unknown top-level field", "{\n\"version\":1,\n\"extra\":true,\n\"catalogs\":[]}", "", `unknown field "extra"`, 3, 1},
		{"unknown nested field", cfg(`{"id":"f1","name":"Код","type":"string","colour":"red"}`), "", `unknown field "colour"`, 1, 0},
		{"wrong field type", cfg(`{"id":5,"name":"Код","type":"string"}`), "catalogs.fields.id", "wrong type", 1, 0},
		{"trailing data", cfg(okField) + "\n{}", "", "after top-level value", 2, 1},
		{"no catalogs", `{"version":1,"catalogs":[]}`, "catalogs", "at least one catalog", 0, 0},
		{"no fields", `{"version":1,"catalogs":[{"id":"c1","name":"Т","fields":[]}]}`, "catalogs[0].fields", "has no fields", 0, 0},
		{"duplicate catalog id", `{"version":1,"catalogs":[{"id":"c1","name":"А","fields":[` + okField + `]},{"id":"c1","name":"Б","fields":[{"id":"f2","name":"К","type":"string"}]}]}`, "catalogs[1].id", `duplicate id "c1" (first used at catalogs[0])`, 0, 0},
		{"field id equals catalog id", cfg(`{"id":"c1","name":"Код","type":"string"}`), "catalogs[0].fields[0].id", "duplicate id", 0, 0},
		{"duplicate field id", cfg(okField + `,{"id":"f1","name":"Имя","type":"string"}`), "catalogs[0].fields[1].id", `duplicate id "f1"`, 0, 0},
		{"invalid id digit", cfg(`{"id":"1x","name":"Код","type":"string"}`), "catalogs[0].fields[0].id", "invalid id", 0, 0},
		{"invalid id space", cfg(`{"id":"a b","name":"Код","type":"string"}`), "catalogs[0].fields[0].id", "invalid id", 0, 0},
		{"empty id", cfg(`{"id":"","name":"Код","type":"string"}`), "catalogs[0].fields[0].id", "invalid id", 0, 0},
		{"empty name", cfg(`{"id":"f1","name":"","type":"string"}`), "catalogs[0].fields[0].name", "name is required", 0, 0},
		{"duplicate field name", cfg(okField + `,{"id":"f2","name":"КОД","type":"string"}`), "catalogs[0].fields[1].name", `duplicate field name "КОД"`, 0, 0},
		{"duplicate catalog name", `{"version":1,"catalogs":[{"id":"c1","name":"Товары","fields":[` + okField + `]},{"id":"c2","name":"товары","fields":[{"id":"f2","name":"К","type":"string"}]}]}`, "catalogs[1].name", "duplicate catalog name", 0, 0},
		{"unknown type", cfg(`{"id":"f1","name":"Код","type":"decimal"}`), "catalogs[0].fields[0].type", `unknown type "decimal"`, 0, 0},
		{"missing type", cfg(`{"id":"f1","name":"Код"}`), "catalogs[0].fields[0].type", `unknown type ""`, 0, 0},
		{"number without digits", cfg(`{"id":"f1","name":"Код","type":"number"}`), "catalogs[0].fields[0].digits", "between 1 and 32", 0, 0},
		{"digits too large", cfg(`{"id":"f1","name":"Код","type":"number","digits":33,"scale":0}`), "catalogs[0].fields[0].digits", "got 33", 0, 0},
		{"scale above digits", cfg(`{"id":"f1","name":"Код","type":"number","digits":5,"scale":6}`), "catalogs[0].fields[0].scale", "between 0 and digits (5)", 0, 0},
		{"digits on string", cfg(`{"id":"f1","name":"Код","type":"string","digits":5}`), "catalogs[0].fields[0]", "only valid for type number", 0, 0},
		{"ref without target", cfg(`{"id":"f1","name":"Код","type":"ref"}`), "catalogs[0].fields[0].target", "target is required", 0, 0},
		{"broken reference", cfg(`{"id":"f1","name":"Код","type":"ref","target":"nope"}`), "catalogs[0].fields[0].target", `broken reference: no catalog with id "nope"`, 0, 0},
		{"target on string", cfg(`{"id":"f1","name":"Код","type":"string","target":"c1"}`), "catalogs[0].fields[0]", "only valid for type ref", 0, 0},
		{"unique on number", cfg(`{"id":"f1","name":"Код","type":"number","digits":5,"scale":0,"unique":true}`), "catalogs[0].fields[0].unique", "only supported for type string", 0, 0},
		{"unique on ref", cfg(`{"id":"f1","name":"Код","type":"ref","target":"c1","unique":true}`), "catalogs[0].fields[0].unique", "only supported for type string", 0, 0},
		{"required on string", cfg(`{"id":"f1","name":"Код","type":"string","required":true}`), "catalogs[0].fields[0].required", "only supported for type ref", 0, 0},
		{"positive on string", cfg(`{"id":"f1","name":"Код","type":"string","positive":true}`), "catalogs[0].fields[0].positive", "only supported for type number", 0, 0},
		{"reference to a field id", cfg(okField + `,{"id":"f2","name":"Ссылка","type":"ref","target":"f1"}`), "catalogs[0].fields[1].target", "broken reference", 0, 0},
	}
	for _, c := range cases {
		_, err := Parse("m.json", []byte(c.src))
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: error = %v, want *Error", c.name, err)
			continue
		}
		wantPath := c.path
		if e.Path != wantPath || !strings.Contains(e.Msg, c.msg) || (c.line == 0) != (e.Line == 0) || (c.line != 0 && e.Line != c.line) || (c.col != 0 && e.Col != c.col) {
			t.Errorf("%s: got path=%q %d:%d msg=%q; want path=%q %d:%d containing %q", c.name, e.Path, e.Line, e.Col, e.Msg, wantPath, c.line, c.col, c.msg)
		}
		if !strings.HasPrefix(e.Error(), "m.json") {
			t.Errorf("%s: Error() = %q", c.name, e.Error())
		}
	}
	if _, err := Parse("m.json", []byte("{\"version\":1,\"x\":\"\xff\"}")); err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Errorf("invalid UTF-8: %v", err)
	}
}

func TestErrorFormat(t *testing.T) {
	e := &Error{File: "d/metadata.json", Line: 3, Col: 7, Path: "catalogs[0].id", Msg: "boom"}
	if got, want := e.Error(), "d/metadata.json:3:7: catalogs[0].id: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	e = &Error{File: "f", Msg: "boom"}
	if got := e.Error(); got != "f: boom" {
		t.Errorf("Error() = %q", got)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("Load of a missing directory succeeded")
	}
}

// Неудачная загрузка не меняет действующую модель.
func TestStoreKeepsModelOnError(t *testing.T) {
	var s Store
	if s.Current() != nil {
		t.Fatal("new store has a model")
	}
	if err := s.Load(writeConfig(t, `{"version":1,"catalogs":[]}`)); err == nil {
		t.Fatal("bad config accepted")
	}
	if s.Current() != nil {
		t.Fatal("failed first load set a model")
	}
	if err := s.Load(smallStock); err != nil {
		t.Fatal(err)
	}
	first := s.Current()
	for _, bad := range []string{"", "{", `{"version":9}`, cfg(`{"id":"f","name":"К","type":"nope"}`)} {
		if err := s.Load(writeConfig(t, bad)); err == nil {
			t.Fatalf("bad config accepted: %q", bad)
		}
		if s.Current() != first {
			t.Fatalf("failed load replaced the active model (%q)", bad)
		}
	}
	other := writeConfig(t, `{"version":1,"catalogs":[{"id":"x","name":"Другой","fields":[{"id":"y","name":"П","type":"boolean"}]}]}`)
	if err := s.Load(other); err != nil {
		t.Fatal(err)
	}
	if s.Current() == first || s.Current().Catalogs[0].ID != "x" {
		t.Error("successful load did not replace the model")
	}
}

func docCfg(docs string) string {
	return `{"version":1,"catalogs":[{"id":"c1","name":"Товары","fields":[` + okField + `]}],"documents":[` + docs + `]}`
}

const (
	numberField = `{"id":"d-num","name":"Номер","type":"string","unique":true}`
	dateField   = `{"id":"d-date","name":"Дата","type":"date"}`
	goodTable   = `{"id":"d-items","name":"Строки","fields":[{"id":"d-prod","name":"Товар","type":"ref","target":"c1","required":true},{"id":"d-qty","name":"Количество","type":"number","digits":15,"scale":3,"positive":true}]}`
)

func docWith(fields, tables string) string {
	return `{"id":"d1","name":"Поступление","fields":[` + fields + `],"tables":[` + tables + `]}`
}

func TestDocumentsLoad(t *testing.T) {
	m, err := Parse("m.json", []byte(docCfg(docWith(numberField+","+dateField, goodTable))))
	if err != nil {
		t.Fatal(err)
	}
	d := m.Documents[0]
	if d.ID != "d1" || len(d.Fields) != 2 || d.Fields[1].Type != TypeDate || len(d.Tables) != 1 {
		t.Fatalf("document = %+v", d)
	}
	f := d.Tables[0].Fields
	if !f[0].Required || !f[1].Positive || f[1].TypeString() != "number(15,3)" || d.Fields[1].TypeString() != "date" {
		t.Errorf("table fields = %+v", f)
	}
	// документ без табличных частей допустим
	if _, err := Parse("m.json", []byte(docCfg(`{"id":"d1","name":"Заметка","fields":[`+numberField+`]}`))); err != nil {
		t.Errorf("document without tables: %v", err)
	}
	// конфигурация без документов по-прежнему допустима (P02)
	if _, err := Parse("m.json", []byte(cfg(okField))); err != nil {
		t.Errorf("no documents: %v", err)
	}
}

func TestDocumentDiagnostics(t *testing.T) {
	cases := []struct {
		name, src, path, msg string
	}{
		{"no header fields", docCfg(docWith("", goodTable)), "documents[0].fields", "has no header fields"},
		{"empty name", docCfg(`{"id":"d1","name":"","fields":[` + numberField + `]}`), "documents[0].name", "name is required"},
		{"duplicate id with catalog", docCfg(`{"id":"c1","name":"Док","fields":[` + numberField + `]}`), "documents[0].id", `duplicate id "c1"`},
		{"duplicate document name", docCfg(docWith(numberField, "") + `,{"id":"d2","name":"поступление","fields":[{"id":"x","name":"Н","type":"string"}]}`), "documents[1].name", "duplicate document name"},
		{"duplicate header field name", docCfg(docWith(numberField+`,{"id":"d-n2","name":"номер","type":"string"}`, "")), "documents[0].fields[1].name", "duplicate field name"},
		{"duplicate header id", docCfg(docWith(numberField+`,{"id":"d-num","name":"Другой","type":"string"}`, "")), "documents[0].fields[1].id", `duplicate id "d-num"`},
		{"table without fields", docCfg(docWith(numberField, `{"id":"t1","name":"Строки","fields":[]}`)), "documents[0].tables[0].fields", "has no fields"},
		{"table id equals field id", docCfg(docWith(numberField, `{"id":"d-num","name":"Строки","fields":[`+okField+`]}`)), "documents[0].tables[0].id", `duplicate id "d-num"`},
		{"duplicate table name", docCfg(docWith(numberField, goodTable+`,{"id":"t2","name":"строки","fields":[{"id":"t2f","name":"Ф","type":"string"}]}`)), "documents[0].tables[1].name", "duplicate table name"},
		{"broken reference in a table", docCfg(docWith(numberField, `{"id":"t1","name":"Строки","fields":[{"id":"t1p","name":"Товар","type":"ref","target":"nope"}]}`)), "documents[0].tables[0].fields[0].target", "broken reference"},
		{"unknown type in a table", docCfg(docWith(numberField, `{"id":"t1","name":"Строки","fields":[{"id":"t1p","name":"Т","type":"money"}]}`)), "documents[0].tables[0].fields[0].type", "unknown type"},
		{"unique on a date", docCfg(docWith(`{"id":"dd","name":"Дата","type":"date","unique":true}`, "")), "documents[0].fields[0].unique", "only supported for type string"},
	}
	for _, c := range cases {
		_, err := Parse("m.json", []byte(c.src))
		var e *Error
		if !errors.As(err, &e) || e.Path != c.path || !strings.Contains(e.Msg, c.msg) {
			t.Errorf("%s: got %v; want path %q containing %q", c.name, err, c.path, c.msg)
		}
	}
}

func regCfg(regs string) string {
	return `{"version":1,"catalogs":[{"id":"c1","name":"Товары","fields":[` + okField + `]}],"registers":[` + regs + `]}`
}

const (
	dimField = `{"id":"r-prod","name":"Товар","type":"ref","target":"c1","required":true}`
	resField = `{"id":"r-qty","name":"Количество","type":"number","digits":15,"scale":3}`
)

func regWith(dims, res string) string {
	return `{"id":"r1","name":"Запасы","dimensions":[` + dims + `],"resources":[` + res + `]}`
}

func TestRegistersLoad(t *testing.T) {
	m, err := Parse("m.json", []byte(regCfg(regWith(dimField, resField))))
	if err != nil {
		t.Fatal(err)
	}
	r := m.Registers[0]
	if r.ID != "r1" || len(r.Dimensions) != 1 || len(r.Resources) != 1 || r.Resources[0].TypeString() != "number(15,3)" {
		t.Errorf("register = %+v", r)
	}
	// строковое и булево измерения допустимы
	if _, err := Parse("m.json", []byte(regCfg(regWith(dimField+`,{"id":"r-s","name":"Склад","type":"string"},{"id":"r-b","name":"Флаг","type":"boolean"}`, resField)))); err != nil {
		t.Errorf("string and boolean dimensions: %v", err)
	}
}

func TestRegisterDiagnostics(t *testing.T) {
	cases := []struct {
		name, src, path, msg string
	}{
		{"no dimensions", regCfg(regWith("", resField)), "registers[0].dimensions", "has no dimensions"},
		{"no resources", regCfg(regWith(dimField, "")), "registers[0].resources", "has no resources"},
		{"empty name", regCfg(`{"id":"r1","name":"","dimensions":[` + dimField + `],"resources":[` + resField + `]}`), "registers[0].name", "name is required"},
		{"duplicate id", regCfg(`{"id":"c1","name":"Р","dimensions":[` + dimField + `],"resources":[` + resField + `]}`), "registers[0].id", `duplicate id "c1"`},
		{"duplicate register name", regCfg(regWith(dimField, resField) + `,{"id":"r2","name":"запасы","dimensions":[{"id":"x1","name":"А","type":"string"}],"resources":[{"id":"x2","name":"Б","type":"number","digits":5,"scale":0}]}`), "registers[1].name", "duplicate register name"},
		{"number dimension", regCfg(regWith(`{"id":"r-n","name":"Число","type":"number","digits":5,"scale":0}`, resField)), "registers[0].dimensions[0].type", "cannot be a dimension"},
		{"string resource", regCfg(regWith(dimField, `{"id":"r-s","name":"Текст","type":"string"}`)), "registers[0].resources[0].type", "must be a number"},
		{"resource repeats dimension name", regCfg(regWith(dimField, `{"id":"r-q2","name":"товар","type":"number","digits":5,"scale":0}`)), "registers[0].resources[0].name", "repeats a dimension name"},
		{"broken dimension reference", regCfg(regWith(`{"id":"r-p","name":"Товар","type":"ref","target":"nope"}`, resField)), "registers[0].dimensions[0].target", "broken reference"},
		{"resource id repeats dimension id", regCfg(regWith(dimField, `{"id":"r-prod","name":"Кол","type":"number","digits":5,"scale":0}`)), "registers[0].resources[0].id", `duplicate id "r-prod"`},
		{"resource digits missing", regCfg(regWith(dimField, `{"id":"r-q","name":"Кол","type":"number"}`)), "registers[0].resources[0].digits", "between 1 and 32"},
	}
	for _, c := range cases {
		_, err := Parse("m.json", []byte(c.src))
		var e *Error
		if !errors.As(err, &e) || e.Path != c.path || !strings.Contains(e.Msg, c.msg) {
			t.Errorf("%s: got %v; want path %q containing %q", c.name, err, c.path, c.msg)
		}
	}
}

func TestUnpostingNeedsPosting(t *testing.T) {
	const doc = `{"version":1,"catalogs":[{"id":"c","name":"К","fields":[{"id":"cf","name":"КФ","type":"string"}]}],"documents":[{"id":"d","name":"Д","fields":[{"id":"f","name":"Ф","type":"string"}],%s}]}`
	if _, err := Parse("m.json", []byte(strings.Replace(doc, "%s", `"unposting":{"file":"a.bsl","procedure":"П"}`, 1))); err == nil || !strings.Contains(err.Error(), "unposting handler but no posting handler") {
		t.Errorf("unposting without posting: %v", err)
	}
	if _, err := Parse("m.json", []byte(strings.Replace(doc, "%s", `"posting":{"file":"a.bsl","procedure":"П"},"unposting":{"file":"../a.bsl","procedure":"П"}`, 1))); err == nil {
		t.Error("unposting path outside the configuration accepted")
	}
	if _, err := Parse("m.json", []byte(strings.Replace(doc, "%s", `"posting":{"file":"a.bsl","procedure":"П"},"unposting":{"file":"b.bsl","procedure":"П"}`, 1))); err != nil {
		t.Errorf("valid unposting: %v", err)
	}
}
