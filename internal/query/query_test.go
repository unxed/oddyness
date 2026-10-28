package query

import (
	"errors"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

func model(t *testing.T) *metadata.Model {
	t.Helper()
	m, err := metadata.Load("../../testdata/configs/small-stock")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustParse(t *testing.T, src string) *Query {
	t.Helper()
	q, err := Parse(src, model(t))
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return q
}

func TestParseTypedQuery(t *testing.T) {
	q := mustParse(t, "ВЫБРАТЬ Товар, Количество КАК Остаток\nИЗ РегистрНакопления.Запасы.Остатки\nГДЕ Товар = &Тов И (Количество > 0 ИЛИ НЕ Количество <= -1.5)\nУПОРЯДОЧИТЬ ПО Остаток УБЫВ")
	if q.Register != "stock" || len(q.Columns) != 2 {
		t.Fatalf("query = %+v", q)
	}
	if q.Columns[0].Field.ID != "stock-product" || q.Columns[0].Field.Role != Dimension || q.Columns[0].Field.Type != (Type{KindRef, "goods"}) {
		t.Errorf("column 0 = %+v", q.Columns[0])
	}
	if q.Columns[1].Name != "Остаток" || q.Columns[1].Field.Role != Resource || q.Columns[1].Field.Type.Kind != KindNumber {
		t.Errorf("column 1 = %+v", q.Columns[1])
	}
	if len(q.Params) != 1 || q.Params[0].Name != "Тов" || q.Params[0].Type != (Type{KindRef, "goods"}) {
		t.Errorf("params = %+v", q.Params)
	}
	and, ok := q.Where.(And)
	if !ok {
		t.Fatalf("where = %#v", q.Where)
	}
	c := and.Left.(Cmp)
	if c.Op != OpEq || c.Right.(ParamOperand).Index != 0 {
		t.Errorf("left = %+v", c)
	}
	or := and.Right.(Or)
	gt := or.Left.(Cmp)
	if gt.Op != OpGt || gt.Right.(Const).Num.Cmp(value.FromInt64(0)) != 0 {
		t.Errorf("gt = %+v", gt)
	}
	not := or.Right.(Not).X.(Cmp)
	if not.Op != OpLe || not.Right.(Const).Num.String() != "-1.5" {
		t.Errorf("not = %+v", not)
	}
	if len(q.Order) != 1 || q.Order[0].Column != 1 || !q.Order[0].Desc {
		t.Errorf("order = %+v", q.Order)
	}
}

func TestEnglishKeywords(t *testing.T) {
	q := mustParse(t, "select Товар as P, Количество from AccumulationRegister.Запасы.Balance where Количество >= 1 and not Товар <> &x order by Количество desc, P asc")
	if q.Columns[0].Name != "P" || len(q.Order) != 2 || q.Order[1].Desc {
		t.Errorf("query = %+v", q)
	}
}

func TestNoWhereIsNil(t *testing.T) {
	q := mustParse(t, "ВЫБРАТЬ Количество ИЗ РегистрНакопления.Запасы.Остатки // комментарий")
	if q.Where != nil || len(q.Params) != 0 {
		t.Errorf("query = %+v", q)
	}
}

func TestParamReuseAndCase(t *testing.T) {
	q := mustParse(t, "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &П ИЛИ &п = Товар")
	if len(q.Params) != 1 {
		t.Fatalf("params = %+v", q.Params)
	}
}

func TestDiagnostics(t *testing.T) {
	cases := []struct {
		name, src string
		pos       Pos
		msg       string
		unsup     bool
	}{
		{"unknown field", "ВЫБРАТЬ Товар, Цвет\nИЗ РегистрНакопления.Запасы.Остатки", Pos{1, 16}, `unknown field "Цвет"`, false},
		{"unknown field in where", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки\nГДЕ Вес = 1", Pos{2, 5}, `unknown field "Вес"`, false},
		{"unknown register", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Нет.Остатки", Pos{1, 34}, `unknown register "Нет"`, false},
		{"unknown source", "ВЫБРАТЬ Товар ИЗ Таблица.Запасы.Остатки", Pos{1, 19}, "unknown source kind", false},
		{"catalog source", "ВЫБРАТЬ Код ИЗ Справочник.Товары.Остатки", Pos{1, 17}, "not supported", true},
		{"turnovers", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Обороты", Pos{1, 44}, "not supported", true},
		{"unknown vt", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Движения", Pos{1, 44}, "unknown virtual table", false},
		{"star", "ВЫБРАТЬ * ИЗ РегистрНакопления.Запасы.Остатки", Pos{1, 9}, "SELECT *", true},
		{"join", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки СОЕДИНЕНИЕ Х", Pos{1, 55}, "not supported", true},
		{"group by", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки СГРУППИРОВАТЬ ПО Товар", Pos{1, 55}, "not supported", true},
		{"distinct", "ВЫБРАТЬ РАЗЛИЧНЫЕ Товар ИЗ РегистрНакопления.Запасы.Остатки", Pos{1, 9}, "not supported", true},
		{"function", "ВЫБРАТЬ СУММА(Количество) ИЗ РегистрНакопления.Запасы.Остатки", Pos{1, 9}, "function calls", true},
		{"in", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар В (1)", Pos{1, 66}, "not supported", true},
		{"arith", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество + 1 > 2", Pos{1, 72}, "not supported", true},
		{"qualified", "ВЫБРАТЬ Запасы.Товар ИЗ РегистрНакопления.Запасы.Остатки", Pos{1, 14}, "qualified", true},
		{"date literal", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество > '20261027'", Pos{1, 73}, "date literals", true},
		{"missing from", "ВЫБРАТЬ Товар", Pos{1, 14}, "unexpected end", false},
		{"trailing", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки Хвост", Pos{1, 55}, "unexpected", false},
		{"no operator", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар", Pos{1, 61}, "unexpected end", false},
		{"bad char", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = #", Pos{1, 66}, "unexpected character", false},
		{"unterminated", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = \"abc", Pos{1, 66}, "unterminated", false},
		{"string vs number", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество = \"1\"", Pos{1, 70}, "cannot compare number with string", false},
		{"ref vs number", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = 5", Pos{1, 67}, "cannot compare", false},
		{"ref ordering", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар > &Т", Pos{1, 67}, "only = and <>", false},
		{"two consts", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ 1 = 1", Pos{1, 63}, "two constants", true},
		{"two params", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ &а = &б", Pos{1, 64}, "two parameters", true},
		{"param conflict", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &а И Количество > &а", Pos{1, 82}, "used as ref(goods) and as number", false},
		{"duplicate column", "ВЫБРАТЬ Товар, Количество КАК товар ИЗ РегистрНакопления.Запасы.Остатки", Pos{1, 33}, "duplicate result column", false},
		{"order not selected", "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки УПОРЯДОЧИТЬ ПО Количество", Pos{1, 69}, "not in the select list", false},
		{"reserved as field", "ВЫБРАТЬ ГДЕ ИЗ РегистрНакопления.Запасы.Остатки", Pos{1, 9}, "unexpected", false},
	}
	m := model(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.src, m)
			var qe *Error
			if !errors.As(err, &qe) {
				t.Fatalf("err = %v, want *Error", err)
			}
			if qe.Pos != c.pos || !strings.Contains(qe.Msg, c.msg) || qe.Unsupported != c.unsup {
				t.Errorf("got %v (unsupported=%v), want %v containing %q (unsupported=%v)", qe, qe.Unsupported, c.pos, c.msg, c.unsup)
			}
		})
	}
}

// Строка с SQL-метасимволами — всего лишь литерал; в идентификаторе они недопустимы; в параметре — просто значение.
func TestSQLMetacharacters(t *testing.T) {
	m := model(t)
	if _, err := Parse(`ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &т И Количество > 0 ИЛИ Количество = 1; DROP TABLE reg_stock`, m); err == nil {
		t.Fatal("trailing statement accepted")
	}
	if _, err := Parse(`ВЫБРАТЬ "Товар" ИЗ РегистрНакопления.Запасы.Остатки`, m); err == nil {
		t.Fatal("quoted identifier accepted")
	}
	if _, err := Parse(`ВЫБРАТЬ Товар; ИЗ РегистрНакопления.Запасы.Остатки`, m); err == nil {
		t.Fatal("semicolon accepted")
	}
	if _, err := Parse(`ВЫБРАТЬ Товар ИЗ РегистрНакопления."Запасы; --".Остатки`, m); err == nil {
		t.Fatal("quoted register accepted")
	}
	// Метасимволы внутри строкового литерала допустимы как данные, но не к полю-числу.
	if _, err := Parse(`ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Количество = "1'; DROP TABLE x; --"`, m); err == nil {
		t.Fatal("string compared with number accepted")
	}
	// Удвоенная кавычка — литерал; нет нужды в ином экранировании. Проверяем лексер напрямую.
	l := newLexer(`"a""b'; --"`)
	tok, err := l.scan()
	if err != nil || tok.kind != tString || tok.text != `a"b'; --` {
		t.Fatalf("tok = %+v, err = %v", tok, err)
	}
}

func TestNilModel(t *testing.T) {
	if _, err := Parse("ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки", nil); err == nil {
		t.Fatal("nil model accepted")
	}
}

func TestBind(t *testing.T) {
	q := mustParse(t, "ВЫБРАТЬ Товар ИЗ РегистрНакопления.Запасы.Остатки ГДЕ Товар = &Тов И Количество > &Мин")
	ref := value.Ref{Catalog: "goods", ID: 1}
	n := value.FromInt64(2)
	got, err := q.Bind(map[string]any{"тов": ref, "МИН": n})
	if err != nil {
		t.Fatal(err)
	}
	if got["Тов"] != ref || got["Мин"] != n {
		t.Errorf("bound = %v", got)
	}
	if _, err := q.Bind(map[string]any{"Тов": nil, "Мин": n}); err != nil {
		t.Errorf("empty ref refused: %v", err)
	}
	bad := []struct {
		name   string
		params map[string]any
		msg    string
	}{
		{"missing", map[string]any{"Тов": ref}, "&Мин is not set"},
		{"extra", map[string]any{"Тов": ref, "Мин": n, "Лишний": 1}, "unknown parameter &Лишний"},
		{"float", map[string]any{"Тов": ref, "Мин": 2.5}, "expected number, got float64"},
		{"int", map[string]any{"Тов": ref, "Мин": 2}, "expected number, got int"},
		{"string for ref", map[string]any{"Тов": "1", "Мин": n}, "expected ref(goods), got string"},
		{"other catalog", map[string]any{"Тов": value.Ref{Catalog: "x", ID: 1}, "Мин": n}, `reference to "x"`},
		{"duplicate case", map[string]any{"Тов": ref, "тов": ref, "Мин": n}, "given twice"},
	}
	for _, c := range bad {
		_, err := q.Bind(c.params)
		var pe *ErrParam
		if !errors.As(err, &pe) || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.msg)
		}
	}
}

func TestBindStringBoolDate(t *testing.T) {
	// Тип параметра выводится из поля; реквизиты таких типов в регистре small-stock нет, поэтому проверяем checkValue.
	for _, c := range []struct {
		t  Type
		v  any
		ok bool
	}{
		{Type{Kind: KindString}, "x'; --", true},
		{Type{Kind: KindString}, 1, false},
		{Type{Kind: KindBool}, true, true},
		{Type{Kind: KindBool}, "true", false},
		{Type{Kind: KindDate}, "2026-10-27", true},
		{Type{Kind: KindDate}, "27.10.2026", false},
		{Type{Kind: KindDate}, 5, false},
	} {
		err := checkValue(Param{Name: "p", Type: c.t}, c.v)
		if (err == nil) != c.ok {
			t.Errorf("%v %#v: err = %v", c.t, c.v, err)
		}
	}
}

func TestTypeString(t *testing.T) {
	for k, want := range map[Kind]string{KindNumber: "number", KindString: "string", KindBool: "boolean", KindDate: "date", 0: "invalid"} {
		if got := (Type{Kind: k}).String(); got != want {
			t.Errorf("%d: %q", k, got)
		}
	}
}
