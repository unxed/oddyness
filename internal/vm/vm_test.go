package vm

import (
	"context"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/bsl"
	"github.com/unxed/oddyness/internal/value"
)

func compile(t *testing.T, src string) *Program {
	t.Helper()
	m, err := bsl.Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p, err := Compile(m)
	if err != nil {
		t.Fatalf("Compile: %v\n%s", err, src)
	}
	return p
}

func num(t *testing.T, s string) Value {
	t.Helper()
	n, err := value.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return NumberValue(n)
}

func eval(t *testing.T, expr string) Value {
	t.Helper()
	p := compile(t, "Функция Ф()\nВозврат "+expr+";\nКонецФункции")
	v, err := p.Run(context.Background(), "ф", nil, Options{})
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return v
}

func evalErr(t *testing.T, expr string) *Error {
	t.Helper()
	p := compile(t, "Функция Ф()\nВозврат "+expr+";\nКонецФункции")
	v, err := p.Run(context.Background(), "Ф", nil, Options{})
	if err == nil {
		t.Fatalf("%s = %s, want runtime error", expr, v)
	}
	e, ok := err.(*Error)
	if !ok || !e.Runtime {
		t.Fatalf("%s: error %T %v is not a runtime *Error", expr, err, err)
	}
	return e
}

func TestExpressions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"1 + 2", "3"},
		{"0.1 + 0.2", "0.3"},
		{"10.500 - 2.250", "8.250"},
		{"1 - 3", "-2"},
		{"-(1 + 2)", "-3"},
		{"- -5", "5"},
		{"+7", "7"},
		{`"a" + "b"`, "ab"},
		{`"x" + """q"""`, `x"q"`},
		{"1 < 2", "Истина"},
		{"2 <= 2.0", "Истина"},
		{"3 > 4", "Ложь"},
		{"4 >= 5", "Ложь"},
		{`"a" < "b"`, "Истина"},
		{"1.0 = 1", "Истина"},
		{"1 <> 1.00", "Ложь"},
		{`1 = "1"`, "Ложь"},
		{`1 <> "1"`, "Истина"},
		{"Неопределено = Неопределено", "Истина"},
		{"Null = Неопределено", "Ложь"},
		{"Null <> Неопределено", "Истина"},
		{"Истина = Истина", "Истина"},
		{"НЕ Истина", "Ложь"},
		{"Истина И Ложь", "Ложь"},
		{"Ложь ИЛИ Истина", "Истина"},
		{"Истина И Истина ИЛИ Ложь", "Истина"},
		// короткое вычисление: правая часть не выполняется
		{`Ложь И (1 < "a")`, "Ложь"},
		{`Истина ИЛИ (1 < "a")`, "Истина"},
		{"Неопределено", "Неопределено"},
		{"Null", "Null"},
	}
	for _, c := range cases {
		if got := eval(t, c.expr).String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	if k := eval(t, "1 + 2").Kind(); k != Number {
		t.Errorf("kind = %v", k)
	}
	if k := eval(t, "1 < 2").Kind(); k != Bool {
		t.Errorf("kind = %v", k)
	}
}

func TestRuntimeErrors(t *testing.T) {
	cases := []struct{ expr, msg string }{
		{`1 + "a"`, "cannot add Number and String"},
		{`"a" - "b"`, "cannot subtract String and String"},
		{`-"a"`, "cannot negate String"},
		{`+"a"`, "unary + needs a Number"},
		{"НЕ 1", "Not needs a Boolean"},
		{"1 И Истина", "And/Or operand must be Boolean"},
		{"Истина ИЛИ 1", ""}, // ИЛИ короткозамкнут: ошибки нет
		{"Ложь ИЛИ 1", "And/Or operand must be Boolean"},
		{`1 < "a"`, "cannot compare Number and String"},
		{"Истина < Ложь", "cannot compare Boolean and Boolean"},
		{"Неопределено > 1", "cannot compare Undefined and Number"},
		{"99999999999999999999999999999999 + 1", "overflow"},
	}
	for _, c := range cases {
		if c.msg == "" {
			eval(t, c.expr)
			continue
		}
		if e := evalErr(t, c.expr); !strings.Contains(e.Msg, c.msg) || e.Pos.Line != 2 {
			t.Errorf("%s: %v, want message containing %q at line 2", c.expr, e, c.msg)
		}
	}
}

func TestFunctionsAndLocals(t *testing.T) {
	p := compile(t, `Функция Сумма(Знач А, Знач Б)
	Возврат А + Б;
КонецФункции

Функция Двойная(Знач Х)
	Перем Р;
	Р = Сумма(Х, Х);
	Возврат Р;
КонецФункции

Функция БезВозврата()
	Перем А;
КонецФункции

Функция Локальная()
	Перем А;
	Возврат А = Неопределено;
КонецФункции

Процедура Пусто(Знач П)
	Возврат;
	П = 1;
КонецПроцедуры

Функция ВызовПроцедуры()
	Пусто(1);
	Возврат "после";
КонецФункции

Функция Присвоение(Знач А)
	А = А + 1;
	Возврат А;
КонецФункции
`)
	run := func(name string, args ...Value) string {
		t.Helper()
		v, err := p.Run(context.Background(), name, args, Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v.String()
	}
	// 10,500 + 2,250 = 12,750 (сценарий складского примера: точные дроби)
	if got := run("Сумма", num(t, "10.500"), num(t, "2.250")); got != "12.750" {
		t.Errorf("Сумма = %q", got)
	}
	if got := run("двойная", num(t, "0.1")); got != "0.2" {
		t.Errorf("Двойная = %q", got)
	}
	if got := run("БезВозврата"); got != "Неопределено" {
		t.Errorf("БезВозврата = %q", got)
	}
	if got := run("Локальная"); got != "Истина" {
		t.Errorf("Локальная = %q", got)
	}
	if got := run("Пусто", num(t, "1")); got != "Неопределено" {
		t.Errorf("Пусто = %q", got)
	}
	if got := run("ВызовПроцедуры"); got != "после" {
		t.Errorf("ВызовПроцедуры = %q", got)
	}
	if got := run("Присвоение", num(t, "41")); got != "42" {
		t.Errorf("Присвоение = %q", got)
	}
	// аргумент вызывающего не меняется
	if got := run("Сумма", StringValue("a"), StringValue("b")); got != "ab" {
		t.Errorf("Сумма строк = %q", got)
	}
}

func TestRunEntryErrors(t *testing.T) {
	p := compile(t, "Функция Ф(Знач А)\nВозврат А;\nКонецФункции")
	for name, args := range map[string][]Value{"Нет": nil, "Ф": nil} {
		if _, err := p.Run(context.Background(), name, args, Options{}); err == nil {
			t.Errorf("Run(%q, %d args) succeeded", name, len(args))
		}
	}
	if _, err := p.Run(context.Background(), "Ф", []Value{{}, {}}, Options{}); err == nil {
		t.Error("two arguments accepted")
	}
}

func TestLimitsAndCancel(t *testing.T) {
	p := compile(t, "Функция Р()\nВозврат Р();\nКонецФункции\nФункция Д()\nВозврат 1 + 2 + 3 + 4;\nКонецФункции")
	_, err := p.Run(context.Background(), "Р", nil, Options{})
	if e, ok := err.(*Error); !ok || !strings.Contains(e.Msg, "call depth limit") {
		t.Errorf("recursion: %v", err)
	}
	if _, err := p.Run(context.Background(), "Д", nil, Options{MaxSteps: 3}); err == nil || !strings.Contains(err.Error(), "step limit 3 exceeded") {
		t.Errorf("step limit: %v", err)
	}
	if v, err := p.Run(context.Background(), "Д", nil, Options{MaxSteps: 100}); err != nil || v.String() != "10" {
		t.Errorf("within limit: %v, %v", v, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Run(ctx, "Д", nil, Options{}); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("cancel: %v", err)
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
		msg       string
	}{
		{"unknown name", "Функция Ф()\nВозврат Икс;\nКонецФункции", 2, 9, `unknown name "Икс"`},
		{"unknown routine", "Функция Ф()\nВозврат Г(1);\nКонецФункции", 2, 9, `unknown routine "Г"`},
		{"arg count", "Функция Г(А)\nВозврат А;\nКонецФункции\nФункция Ф()\nВозврат Г();\nКонецФункции", 5, 9, "takes 1 arguments, got 0"},
		{"procedure value", "Процедура П()\nКонецПроцедуры\nФункция Ф()\nВозврат П();\nКонецФункции", 4, 9, "does not return a value"},
		{"by-reference assign", "Процедура П(А)\nА = 1;\nКонецПроцедуры", 2, 1, "by-reference parameter"},
		{"undeclared assign", "Процедура П()\nА = 1;\nКонецПроцедуры", 2, 1, "undeclared variable"},
		{"multiply", "Функция Ф()\nВозврат 2 * 3;\nКонецФункции", 2, 11, "operator * is not supported yet"},
		{"divide", "Функция Ф()\nВозврат 6 / 3;\nКонецФункции", 2, 11, "operator / is not supported yet"},
		{"module var", "Перем А;\nПроцедура П()\nКонецПроцедуры", 1, 1, "module-level variables"},
		{"local duplicates param", "Процедура П(А)\nПерем а;\nКонецПроцедуры", 2, 1, "duplicates"},
	}
	for _, c := range cases {
		m, err := bsl.Parse(c.src)
		if err != nil {
			t.Fatalf("%s: parse: %v", c.name, err)
		}
		_, err = Compile(m)
		e, ok := err.(*Error)
		if !ok || e.Runtime || e.Pos.Line != c.line || e.Pos.Col != c.col || !strings.Contains(e.Msg, c.msg) {
			t.Errorf("%s: got %v; want %d:%d containing %q", c.name, err, c.line, c.col, c.msg)
		}
	}
}

func TestZeroValueIsUndefined(t *testing.T) {
	var v Value
	if v.Kind() != Undefined || v.String() != "Неопределено" {
		t.Errorf("zero Value = %v %q", v.Kind(), v.String())
	}
}
