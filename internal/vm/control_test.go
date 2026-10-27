package vm

import (
	"context"
	"strings"
	"testing"
)

const flowProgram = `Функция Сумма(Знач Н)
	Перем Инд, Сум;
	Инд = 1;
	Сум = 0;
	Пока Инд <= Н Цикл
		Сум = Сум + Инд;
		Инд = Инд + 1;
	КонецЦикла;
	Возврат Сум;
КонецФункции

Функция Знак(Знач Х)
	Если Х < 0 Тогда
		Возврат "минус";
	ИначеЕсли Х = 0 Тогда
		Возврат "нуль";
	Иначе
		Возврат "плюс";
	КонецЕсли;
КонецФункции

Функция БезИначе(Знач Х)
	Перем Р;
	Р = "не вошли";
	Если Х > 10 Тогда
		Р = "вошли";
	КонецЕсли;
	Возврат Р;
КонецФункции

Функция НечётныеДо8()
	Перем Инд, Сум;
	Инд = 0;
	Сум = 0;
	Пока Истина Цикл
		Инд = Инд + 1;
		Если Инд > 7 Тогда
			Прервать;
		КонецЕсли;
		Если Инд % 2 = 0 Тогда
			Продолжить;
		КонецЕсли;
		Сум = Сум + Инд;
	КонецЦикла;
	Возврат Сум;
КонецФункции

Функция Таблица()
	Перем И1, И2, Сум;
	Сум = 0;
	И1 = 1;
	Пока И1 <= 3 Цикл
		И2 = 1;
		Пока И2 <= 3 Цикл
			Сум = Сум + И1 * И2;
			И2 = И2 + 1;
		КонецЦикла;
		И1 = И1 + 1;
	КонецЦикла;
	Возврат Сум;
КонецФункции

Функция Факт(Знач Н)
	Если Н <= 1 Тогда
		Возврат 1;
	КонецЕсли;
	Возврат Н * Факт(Н - 1);
КонецФункции

Функция Фиб(Знач Н)
	Если Н < 2 Тогда
		Возврат Н;
	КонецЕсли;
	Возврат Фиб(Н - 1) + Фиб(Н - 2);
КонецФункции

Функция Бесконечный()
	Пока Истина Цикл
	КонецЦикла;
КонецФункции

Функция ЧисловоеУсловие()
	Если 1 Тогда
		Возврат 1;
	КонецЕсли;
КонецФункции

Функция ЧисловоеУсловиеЦикла()
	Пока 0 Цикл
	КонецЦикла;
КонецФункции
`

func TestControlFlow(t *testing.T) {
	p := compile(t, flowProgram)
	run := func(name string, args ...Value) string {
		t.Helper()
		v, err := p.Run(context.Background(), name, args, Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v.String()
	}
	cases := []struct {
		name string
		args []Value
		want string
	}{
		{"Сумма", []Value{num(t, "10")}, "55"},
		{"Сумма", []Value{num(t, "0")}, "0"},
		{"Сумма", []Value{num(t, "100")}, "5050"},
		{"Знак", []Value{num(t, "-2.5")}, "минус"},
		{"Знак", []Value{num(t, "0.00")}, "нуль"},
		{"Знак", []Value{num(t, "0.1")}, "плюс"},
		{"БезИначе", []Value{num(t, "11")}, "вошли"},
		{"БезИначе", []Value{num(t, "10")}, "не вошли"},
		{"НечётныеДо8", nil, "16"},
		{"Таблица", nil, "36"},
		{"Факт", []Value{num(t, "5")}, "120"},
		{"Факт", []Value{num(t, "20")}, "2432902008176640000"},
		{"Фиб", []Value{num(t, "10")}, "55"},
	}
	for _, c := range cases {
		if got := run(c.name, c.args...); got != c.want {
			t.Errorf("%s%v = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func runErr(t *testing.T, p *Program, ctx context.Context, name string, opt Options, args ...Value) error {
	t.Helper()
	v, err := p.Run(ctx, name, args, opt)
	if err == nil {
		t.Fatalf("%s = %s, want error", name, v)
	}
	return err
}

func TestControlFlowErrors(t *testing.T) {
	p := compile(t, flowProgram)
	bg := context.Background()
	if err := runErr(t, p, bg, "Бесконечный", Options{MaxSteps: 1000}); !strings.Contains(err.Error(), "step limit 1000 exceeded") {
		t.Errorf("infinite loop: %v", err)
	}
	if err := runErr(t, p, bg, "Факт", Options{}, num(t, "30")); !strings.Contains(err.Error(), "overflow") {
		t.Errorf("30!: %v", err)
	}
	for _, name := range []string{"ЧисловоеУсловие", "ЧисловоеУсловиеЦикла"} {
		err := runErr(t, p, bg, name, Options{})
		if e, ok := err.(*Error); !ok || !strings.Contains(e.Msg, "condition must be Boolean, got Number") || e.Pos.Line == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	// отмена посреди цикла: контекст отвечает «жив» на первую проверку и «отменён» на следующие
	ctx := &flakyCtx{Context: bg}
	if err := runErr(t, p, ctx, "Бесконечный", Options{}); !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("cancel: %v", err)
	}
	if ctx.calls < 2 {
		t.Errorf("ctx checked %d times", ctx.calls)
	}
}

type flakyCtx struct {
	context.Context
	calls int
}

func (c *flakyCtx) Err() error {
	c.calls++
	if c.calls > 1 {
		return context.Canceled
	}
	return nil
}
