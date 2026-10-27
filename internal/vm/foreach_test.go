package vm

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/bsl"
	"github.com/unxed/oddyness/internal/value"
)

const tableProgram = `Функция СуммаКоличеств(Строки)
	Перем Сум;
	Сум = 0;
	Для Каждого Стр Из Строки Цикл
		Сум = Сум + Стр.Количество;
	КонецЦикла;
	Возврат Сум;
КонецФункции

Функция Названия(Строки)
	Перем Р;
	Р = "";
	Для Каждого С Из Строки Цикл
		Если С.Количество = 0 Тогда
			Продолжить;
		КонецЕсли;
		Если С.Количество < 0 Тогда
			Прервать;
		КонецЕсли;
		Р = Р + С.Товар + ";";
	КонецЦикла;
	Возврат Р;
КонецФункции

Функция Пары(А, Б)
	Перем Н;
	Н = 0;
	Для Каждого Х Из А Цикл
		Для Каждого У Из Б Цикл
			Н = Н + 1;
		КонецЦикла;
	КонецЦикла;
	Возврат Н;
КонецФункции

Функция ЧужаяКолонка(Строки)
	Для Каждого С Из Строки Цикл
		Возврат С.Нет;
	КонецЦикла;
КонецФункции

Функция НеКоллекция(Знач Х)
	Для Каждого С Из Х Цикл
	КонецЦикла;
КонецФункции

Функция ПолеЧисла(Знач Х)
	Возврат Х.Количество;
КонецФункции

Функция Счёт(Знач Н)
	Перем Инд, Сум;
	Инд = 0;
	Сум = 0;
	Пока Инд < Н Цикл
		Инд = Инд + 1;
		Сум = Сум + Инд;
	КонецЦикла;
	Возврат Сум;
КонецФункции
`

func mustTable(t testing.TB, cols []string, rows ...[]Value) Value {
	t.Helper()
	tb, err := NewTable(cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	return TableValue(tb)
}

func nv(t testing.TB, s string) Value {
	t.Helper()
	n, err := value.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return NumberValue(n)
}

func TestForEachOverTable(t *testing.T) {
	p := compile(t, tableProgram)
	cols := []string{"Товар", "Количество"}
	full := mustTable(t, cols,
		[]Value{StringValue("гайка"), nv(t, "10.500")},
		[]Value{StringValue("болт"), nv(t, "2.250")},
		[]Value{StringValue("шайба"), nv(t, "0")},
		[]Value{StringValue("утиль"), nv(t, "-1")},
		[]Value{StringValue("после"), nv(t, "99")},
	)
	empty := mustTable(t, cols)
	upper := mustTable(t, []string{"ТОВАР", "КОЛИЧЕСТВО"},
		[]Value{StringValue("a"), nv(t, "0.1")},
		[]Value{StringValue("b"), nv(t, "0.2")},
	)
	three := mustTable(t, cols, []Value{{}, {}}, []Value{{}, {}}, []Value{{}, {}})
	two := mustTable(t, cols, []Value{{}, {}}, []Value{{}, {}})
	cases := []struct {
		name string
		args []Value
		want string
	}{
		{"СуммаКоличеств", []Value{full}, "110.750"},
		{"СуммаКоличеств", []Value{empty}, "0"},
		{"СуммаКоличеств", []Value{upper}, "0.3"},
		{"Названия", []Value{full}, "гайка;болт;"},
		{"Названия", []Value{empty}, ""},
		{"Названия", []Value{upper}, "a;b;"},
		{"Пары", []Value{three, two}, "6"},
		{"Пары", []Value{three, empty}, "0"},
		{"Пары", []Value{empty, two}, "0"},
		{"Счёт", []Value{nv(t, "100")}, "5050"},
	}
	for _, c := range cases {
		v, err := p.Run(context.Background(), c.name, c.args, Options{})
		if err != nil || v.String() != c.want {
			t.Errorf("%s = %q, %v; want %q", c.name, v.String(), err, c.want)
		}
	}
}

func TestForEachErrors(t *testing.T) {
	p := compile(t, tableProgram)
	one := mustTable(t, []string{"Товар", "Количество"}, []Value{StringValue("x"), nv(t, "1")})
	cases := []struct {
		name string
		arg  Value
		msg  string
	}{
		{"ЧужаяКолонка", one, `unknown column "нет"`},
		{"НеКоллекция", nv(t, "1"), "For Each needs a collection, got Number"},
		{"НеКоллекция", one.tbl.rowsValue(0), "For Each needs a collection, got Row"},
		{"ПолеЧисла", nv(t, "1"), `cannot read field "количество" of Number`},
		{"ПолеЧисла", one, `cannot read field "количество" of Collection`},
	}
	for _, c := range cases {
		_, err := p.Run(context.Background(), c.name, []Value{c.arg}, Options{})
		e, ok := err.(*Error)
		if !ok || !e.Runtime || !strings.Contains(e.Msg, c.msg) || e.Pos.Line == 0 {
			t.Errorf("%s: %v, want runtime error containing %q", c.name, err, c.msg)
		}
	}
}

// rowsValue — помощник теста: значение строки номер i.
func (t *Table) rowsValue(i int) Value { return Value{kind: Row, tbl: t, i: i} }

func TestForEachCompileErrors(t *testing.T) {
	cases := []struct {
		src       string
		line, col int
		msg       string
	}{
		{"Процедура П(Х, Т)\nДля Каждого Х Из Т Цикл\nКонецЦикла;\nКонецПроцедуры", 2, 1, "by-reference parameter"},
		{"Процедура П()\nДля Каждого Х Из Нет Цикл\nКонецЦикла;\nКонецПроцедуры", 2, 18, `unknown name "Нет"`},
		{"Процедура П()\nПерем А;\nА = Б.В;\nКонецПроцедуры", 3, 5, `unknown name "Б"`},
	}
	for _, c := range cases {
		m, err := bsl.Parse(c.src)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(m)
		e, ok := err.(*Error)
		if !ok || e.Pos.Line != c.line || e.Pos.Col != c.col || !strings.Contains(e.Msg, c.msg) {
			t.Errorf("%q: got %v; want %d:%d %q", c.src, err, c.line, c.col, c.msg)
		}
	}
}

func TestNewTableErrors(t *testing.T) {
	v := func(n int) []Value { return make([]Value, n) }
	for name, f := range map[string]func() error{
		"duplicate":  func() error { _, e := NewTable([]string{"А", "а"}, nil); return e },
		"empty name": func() error { _, e := NewTable([]string{"А", ""}, nil); return e },
		"short row":  func() error { _, e := NewTable([]string{"А", "Б"}, [][]Value{v(2), v(1)}); return e },
		"long row":   func() error { _, e := NewTable([]string{"А"}, [][]Value{v(2)}); return e },
	} {
		if f() == nil {
			t.Errorf("%s: no error", name)
		}
	}
	tb, err := NewTable([]string{"А"}, [][]Value{v(1), v(1)})
	if err != nil || tb.Len() != 2 {
		t.Errorf("valid table: %v, len %d", err, tb.Len())
	}
}

// bigTable строит n строк с количеством i%1000 + 0.250 и возвращает таблицу и
// сумму, посчитанную независимо от VM (прямым сложением value.Number).
func bigTable(t testing.TB, n int) (Value, string) {
	t.Helper()
	rows := make([][]Value, n)
	sum := value.FromInt64(0)
	for i := range rows {
		q, err := value.Parse(fmt.Sprintf("%d.250", i%1000))
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []Value{StringValue("т"), NumberValue(q)}
		if sum, err = sum.Add(q); err != nil {
			t.Fatal(err)
		}
	}
	return mustTable(t, []string{"Товар", "Количество"}, rows...), sum.String()
}

// Точный allocation-тест: после прогрева на подготовленных данных числовой цикл
// и перебор строк таблицы не выделяют память; результат проверяется. Компиляция,
// строки, Mul/Div/Mod (math/big) и подготовка данных в обещание не входят.
func TestHotLoopsZeroAllocs(t *testing.T) {
	p := compile(t, tableProgram)
	tbl, wantSum := bigTable(t, 2000)
	ctx := context.Background()
	m := p.NewMachine()
	sumRows, err := p.Entry("СуммаКоличеств")
	if err != nil {
		t.Fatal(err)
	}
	count, err := p.Entry("Счёт")
	if err != nil {
		t.Fatal(err)
	}
	rowsArgs := []Value{tbl}
	countArgs := []Value{nv(t, "1000")}
	var res Value
	for _, c := range []struct {
		name string
		e    Entry
		args []Value
		want string
	}{
		{"СуммаКоличеств", sumRows, rowsArgs, wantSum},
		{"Счёт", count, countArgs, "500500"},
	} {
		if res, err = m.Call(ctx, c.e, c.args, Options{}); err != nil || res.String() != c.want { // прогрев
			t.Fatalf("%s warm-up = %q, %v; want %q", c.name, res.String(), err, c.want)
		}
		allocs := testing.AllocsPerRun(20, func() { res, err = m.Call(ctx, c.e, c.args, Options{}) })
		if allocs != 0 {
			t.Errorf("%s: %v allocs per run, want 0", c.name, allocs)
		}
		if err != nil || res.String() != c.want {
			t.Errorf("%s = %q, %v; want %q", c.name, res.String(), err, c.want)
		}
	}
}

func BenchmarkSumQuantities(b *testing.B) {
	p := compile(b, tableProgram)
	tbl, want := bigTable(b, 1000)
	e, _ := p.Entry("СуммаКоличеств")
	m := p.NewMachine()
	args := []Value{tbl}
	ctx := context.Background()
	var res Value
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err = m.Call(ctx, e, args, Options{})
	}
	b.StopTimer()
	if err != nil || res.String() != want {
		b.Fatalf("result %q, %v; want %q", res.String(), err, want)
	}
}

func BenchmarkCountLoop(b *testing.B) {
	p := compile(b, tableProgram)
	e, _ := p.Entry("Счёт")
	m := p.NewMachine()
	args := []Value{nv(b, "1000")}
	ctx := context.Background()
	var res Value
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err = m.Call(ctx, e, args, Options{})
	}
	b.StopTimer()
	if err != nil || res.String() != "500500" {
		b.Fatalf("result %q, %v", res.String(), err)
	}
}
