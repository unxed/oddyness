package bsl

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Module {
	t.Helper()
	m, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, src)
	}
	return m
}

func parseErr(t *testing.T, src string) *Error {
	t.Helper()
	m, err := Parse(src)
	if err == nil {
		t.Fatalf("Parse succeeded (%d routines), want error\n%s", len(m.Routines), src)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %T is not *Error: %v", err, err)
	}
	return e
}

const programRu = `Перем Счётчик Экспорт;

// комментарий
Функция Сумма(Знач А, Б) Экспорт
	Перем Результат;
	Результат = А + Б * 2;
	Возврат Результат;
КонецФункции

Процедура Привет(Имя)
	Сообщить("Привет, " + Имя + """!"" ");
КонецПроцедуры
`

const programEn = `Var Counter Export;

// comment
Function Sum(Val A, B) Export
	Var Result;
	Result = A + B * 2;
	Return Result;
EndFunction

Procedure Hello(Name)
	Message("Hello, " + Name + """!"" ");
EndProcedure
`

func shape(m *Module) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "vars=%d", len(m.Vars))
	for _, v := range m.Vars {
		fmt.Fprintf(&sb, " var(%d,export=%v)", len(v.Names), v.Export)
	}
	for _, r := range m.Routines {
		fmt.Fprintf(&sb, " %s(params=%d,export=%v,locals=%d,body=%d)", routineWord(r), len(r.Params), r.Export, len(r.Locals), len(r.Body))
	}
	return sb.String()
}

func TestRussianAndEnglishKeywords(t *testing.T) {
	ru, en := mustParse(t, programRu), mustParse(t, programEn)
	want := "vars=1 var(1,export=true) function(params=2,export=true,locals=1,body=2) procedure(params=1,export=false,locals=0,body=1)"
	if got := shape(ru); got != want {
		t.Errorf("ru shape = %q, want %q", got, want)
	}
	if got := shape(en); got != want {
		t.Errorf("en shape = %q, want %q", got, want)
	}
	if !ru.Routines[0].Params[0].ByVal || ru.Routines[0].Params[1].ByVal {
		t.Error("Знач must mark only the first parameter")
	}
	// регистр ключевых слов не важен
	mustParse(t, "ФУНКЦИЯ ф() возврат 1; КОНЕЦФУНКЦИИ")
	mustParse(t, "function f() RETURN 1; endFunction")
}

func exprString(e Expr) string {
	switch e := e.(type) {
	case *NumberLit:
		return e.Text
	case *StringLit:
		return fmt.Sprintf("%q", e.Value)
	case *ConstLit:
		return [...]string{"true", "false", "undefined", "null"}[e.Kind]
	case *Ident:
		return e.Name
	case *Call:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = exprString(a)
		}
		return e.Name + "(" + strings.Join(args, ", ") + ")"
	case *Unary:
		return "(" + e.Op + " " + exprString(e.X) + ")"
	case *Binary:
		return "(" + exprString(e.X) + " " + e.Op + " " + exprString(e.Y) + ")"
	}
	return "?"
}

func returnedExpr(t *testing.T, expr string) Expr {
	t.Helper()
	m := mustParse(t, "Функция Ф()\nВозврат "+expr+";\nКонецФункции")
	return m.Routines[0].Body[0].(*Return).Value
}

func TestExpressionPrecedence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1 + 2 * 3", "(1 + (2 * 3))"},
		{"1 - 2 - 3", "((1 - 2) - 3)"},
		{"8 / 4 % 3", "((8 / 4) % 3)"},
		{"-А * Б", "((- А) * Б)"},
		{"- -1", "(- (- 1))"},
		{"(1 + 2) * 3", "((1 + 2) * 3)"},
		{"НЕ А И Б ИЛИ В", "(((not А) and Б) or В)"},
		{"not a and b or c and d", "(((not a) and b) or (c and d))"},
		{"НЕ А = Б", "(not (А = Б))"},
		{"А = Б + 1", "(А = (Б + 1))"},
		{"А <> Б", "(А <> Б)"},
		{"А <= 1 И Б >= 2", "((А <= 1) and (Б >= 2))"},
		{"Ф(1, Г(2), А + 1)", "Ф(1, Г(2), (А + 1))"},
		{"Ф()", "Ф()"},
		{"Истина", "true"},
		{"false", "false"},
		{"Неопределено", "undefined"},
		{"Null", "null"},
		{`"a""b"`, `"a\"b"`},
	}
	for _, c := range cases {
		if got := exprString(returnedExpr(t, c.in)); got != c.want {
			t.Errorf("%s => %s, want %s", c.in, got, c.want)
		}
	}
}

func TestExactNumberLiterals(t *testing.T) {
	for _, lit := range []string{
		"0.1", "0.30", "7", "1234567890123456789", "12345678901234567890",
		"12345678901234567890123456789012", "0.12345678901234567890123456789012",
	} {
		n, ok := returnedExpr(t, lit).(*NumberLit)
		if !ok {
			t.Fatalf("%s: not a NumberLit", lit)
		}
		if n.Text != lit || n.Value.String() != lit {
			t.Errorf("%s: Text=%q Value=%q", lit, n.Text, n.Value.String())
		}
	}
	// 0.1 + 0.2 разбирается без потери цифр: значение — именно 0.3 при сложении Number
	a := returnedExpr(t, "0.1").(*NumberLit).Value
	b := returnedExpr(t, "0.2").(*NumberLit).Value
	sum, err := a.Add(b)
	if err != nil || sum.String() != "0.3" {
		t.Errorf("0.1 + 0.2 = %q, %v", sum.String(), err)
	}
}

func TestStringLiterals(t *testing.T) {
	s := returnedExpr(t, "\"строка 1\n\t|строка 2\"").(*StringLit)
	if s.Value != "строка 1\nстрока 2" {
		t.Errorf("multiline = %q", s.Value)
	}
	s = returnedExpr(t, `"a // not a comment"`).(*StringLit)
	if s.Value != "a // not a comment" {
		t.Errorf("string with slashes = %q", s.Value)
	}
	if v := returnedExpr(t, "\"x\r\n\t|y\"").(*StringLit).Value; v != "x\ny" {
		t.Errorf("CRLF continuation = %q", v)
	}
}

func TestCommentsAndEmpty(t *testing.T) {
	for _, src := range []string{"", "   \n\t", "// только комментарий", "// a\n// b\n"} {
		if m := mustParse(t, src); len(m.Routines)+len(m.Vars) != 0 {
			t.Errorf("%q: not empty", src)
		}
	}
	m := mustParse(t, "Процедура П()\n\tА = 4 / 2; // деление, не комментарий\nКонецПроцедуры")
	if got := exprString(m.Routines[0].Body[0].(*Assign).Value); got != "(4 / 2)" {
		t.Errorf("division = %s", got)
	}
	// последний оператор без точки с запятой и пустые операторы допустимы
	mustParse(t, "Процедура П()\n\tА = 1\nКонецПроцедуры")
	mustParse(t, "Процедура П()\n\t;;А = 1;;\nКонецПроцедуры")
	mustParse(t, "Процедура П() Экспорт\nКонецПроцедуры")
}

func TestDiagnostics(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		line, col   int
		msg         string
		unsupported bool
	}{
		{"numeric for", "Процедура П()\n  Для А = 1 По 3 Цикл\nКонецПроцедуры", 2, 7, "numeric For loop (only For Each) is not supported", true},
		{"numeric for en", "Procedure P()\nFor X = 1 To 3 Do\nEndProcedure", 2, 5, "numeric For loop", true},
		{"foreach without in", "Процедура П()\nДля Каждого Х Цикл\nКонецЦикла;\nКонецПроцедуры", 2, 15, `expected "Из"`, false},
		{"foreach without do", "Процедура П()\nДля Каждого Х Из Т\nКонецЦикла;\nКонецПроцедуры", 3, 1, `expected "Цикл"`, false},
		{"foreach reserved variable", "Процедура П()\nДля Каждого Если Из Т Цикл\nКонецЦикла;\nКонецПроцедуры", 2, 13, "reserved word", false},
		{"unclosed foreach", "Процедура П()\nДля Каждого Х Из Т Цикл\n", 3, 1, "For Each is not closed", false},
		{"member chain", "Процедура П()\nА = Б.В.Г;\nКонецПроцедуры", 2, 8, "member access and indexing", true},
		{"member keyword field", "Процедура П()\nА = Б.Если;\nКонецПроцедуры", 2, 7, "reserved word", false},
		{"method call", "Процедура П()\nА = Б.В(1);\nКонецПроцедуры", 2, 8, "method call is not supported", true},
		{"if without then", "Процедура П()\nЕсли Истина\nКонецЕсли;\nКонецПроцедуры", 3, 1, `expected "Тогда"`, false},
		{"unclosed if", "Процедура П()\nЕсли Истина Тогда\nКонецПроцедуры", 3, 1, "If: expected closing keyword", false},
		{"unclosed if at eof", "Процедура П()\nЕсли Истина Тогда\n", 3, 1, "If is not closed", false},
		{"while without do", "Процедура П()\nПока Истина\nКонецЦикла;\nКонецПроцедуры", 3, 1, `expected "Цикл"`, false},
		{"unclosed while", "Процедура П()\nПока Истина Цикл\nКонецЕсли;\nКонецПроцедуры", 3, 1, "While: expected closing keyword", false},
		{"break outside loop", "Процедура П()\nПрервать;\nКонецПроцедуры", 2, 1, "outside a loop", false},
		{"continue in if outside loop", "Процедура П()\nЕсли Истина Тогда\nПродолжить;\nКонецЕсли;\nКонецПроцедуры", 3, 1, "outside a loop", false},
		{"stray endif", "Процедура П()\nКонецЕсли;\nКонецПроцедуры", 2, 1, "does not close procedure", false},
		{"var in block", "Процедура П()\nЕсли Истина Тогда\nПерем А;\nКонецЕсли;\nКонецПроцедуры", 3, 1, "must precede statements", false},
		{"return value in procedure branch", "Процедура П()\nЕсли Истина Тогда\nВозврат 1;\nКонецЕсли;\nКонецПроцедуры", 3, 1, "cannot return a value", false},
		{"unsupported try", "Процедура П()\nПопытка\nКонецПроцедуры", 2, 1, `"Попытка" statement`, true},
		{"directive", "&НаСервере\nПроцедура П()\nКонецПроцедуры", 1, 1, "compilation directive &НаСервере is not supported", true},
		{"directive in body", "Процедура П()\n&НаКлиенте\nКонецПроцедуры", 2, 1, "compilation directive &НаКлиенте", true},
		{"preprocessor", "#Если Сервер Тогда\nПроцедура П()\nКонецПроцедуры", 1, 1, "preprocessor instruction", true},
		{"date literal", "Функция Ф()\nВозврат '20250101';\nКонецФункции", 2, 9, "date literals are not supported", true},
		{"new in expression", "Процедура П()\nА = Новый Массив;\nКонецПроцедуры", 2, 5, `"Новый" in an expression`, true},
		{"ternary", "Функция Ф()\nВозврат ?(Истина, 1, 2);\nКонецФункции", 2, 9, "ternary operator", true},
		{"module body statement", "А = 1;", 1, 1, "module body", true},
		{"member access", "Процедура П()\nА.Б = 1;\nКонецПроцедуры", 2, 2, "member access and indexing", true},
		{"member access in call", "Процедура П()\nА().Б;\nКонецПроцедуры", 2, 4, "member access and indexing", true},
		{"index", "Процедура П()\nА = Б[1];\nКонецПроцедуры", 2, 6, "member access and indexing", true},
		{"omitted argument", "Процедура П()\nФ(1,,2);\nКонецПроцедуры", 2, 5, "omitted call argument", true},
		{"default parameter", "Процедура П(А = 1)\nКонецПроцедуры", 1, 15, "default parameter value", true},
		{"unclosed procedure", "Процедура П()\n", 2, 1, `procedure "П" is not closed`, false},
		{"wrong end", "Процедура П()\nКонецФункции", 2, 1, "does not close procedure", false},
		{"function without value", "Функция Ф()\nВозврат;\nКонецФункции", 2, 1, "must return a value", false},
		{"procedure with value", "Процедура П()\nВозврат 1;\nКонецПроцедуры", 2, 1, "cannot return a value", false},
		{"missing semicolon", "Процедура П()\nА = 1\nБ = 2;\nКонецПроцедуры", 3, 1, `expected ";"`, false},
		{"reserved param", "Процедура П(\nКонецПроцедуры", 2, 1, "reserved word", false},
		{"reserved routine name", "Процедура Если()\nКонецПроцедуры", 1, 11, "reserved word", false},
		{"duplicate routine", "Процедура П()\nКонецПроцедуры\nПроцедура п()\nКонецПроцедуры", 3, 1, `already declared at 1:1`, false},
		{"duplicate parameter", "Процедура П(А, а)\nКонецПроцедуры", 1, 16, "declared twice", false},
		{"var after statement", "Процедура П()\nА = 1;\nПерем Б;\nКонецПроцедуры", 3, 1, "must precede statements", false},
		{"local export", "Процедура П()\nПерем А Экспорт;\nКонецПроцедуры", 2, 9, "cannot be exported", false},
		{"bad character", "Процедура П()\nА = 1 @ 2;\nКонецПроцедуры", 2, 7, `unexpected character '@'`, false},
		{"number overflow", "Процедура П()\nА = 123456789012345678901234567890123;\nКонецПроцедуры", 2, 5, "overflow", false},
		{"number with letter", "Процедура П()\nА = 1e5;\nКонецПроцедуры", 2, 5, "invalid number literal", false},
		{"non-associative comparison", "Процедура П()\nА = 1 < 2 < 3;\nКонецПроцедуры", 2, 11, `expected ";"`, false},
		{"missing paren", "Процедура П()\nФ(1;\nКонецПроцедуры", 2, 4, `expected ")"`, false},
		{"unterminated string", "Процедура П()\nА = \"abc;\nКонецПроцедуры", 2, 5, "unterminated string", false},
		{"unterminated string at eof", "Процедура П()\nА = \"abc", 2, 5, "unterminated string", false},
		{"missing expression", "Процедура П()\nА = ;\nКонецПроцедуры", 2, 5, "expected an expression", false},
		{"assignment target", "Процедура П()\nА Б;\nКонецПроцедуры", 2, 3, `expected "=" or "("`, false},
		{"garbage at module level", "123", 1, 1, "expected procedure, function or variable declaration", false},
	}
	for _, c := range cases {
		e := parseErr(t, c.src)
		if e.Pos.Line != c.line || e.Pos.Col != c.col || !strings.Contains(e.Msg, c.msg) || e.Unsupported != c.unsupported {
			t.Errorf("%s: got %d:%d unsupported=%v %q; want %d:%d unsupported=%v containing %q",
				c.name, e.Pos.Line, e.Pos.Col, e.Unsupported, e.Msg, c.line, c.col, c.unsupported, c.msg)
		}
		if want := fmt.Sprintf("%d:%d: ", c.line, c.col); !strings.HasPrefix(e.Error(), want) {
			t.Errorf("%s: Error() = %q, want prefix %q", c.name, e.Error(), want)
		}
	}
}

// Ошибка выбирается по порядку текста: более ранняя ошибка разбора важнее
// более поздней ошибки лексера.
func TestFirstErrorWins(t *testing.T) {
	e := parseErr(t, "Процедура П()\nПопытка\nА = 1 @ 2;\nКонецПроцедуры")
	if e.Pos.Line != 2 || !e.Unsupported {
		t.Errorf("got %d:%d %q", e.Pos.Line, e.Pos.Col, e.Msg)
	}
}

func TestPositionsCountRunes(t *testing.T) {
	// "Ж" — один символ, хотя два байта: колонка считается в символах
	e := parseErr(t, "Процедура П()\nЖЖЖ = @;\nКонецПроцедуры")
	if e.Pos.Line != 2 || e.Pos.Col != 7 {
		t.Errorf("got %d:%d, want 2:7", e.Pos.Line, e.Pos.Col)
	}
}

func TestNodePositions(t *testing.T) {
	m := mustParse(t, "Процедура П()\n\tА = Ф(1) + 2;\nКонецПроцедуры")
	r := m.Routines[0]
	if r.Position() != (Pos{1, 1}) {
		t.Errorf("routine at %v", r.Position())
	}
	as := r.Body[0].(*Assign)
	if as.Position() != (Pos{2, 2}) {
		t.Errorf("assign at %v", as.Position())
	}
	bin := as.Value.(*Binary)
	if bin.Position() != (Pos{2, 11}) || bin.X.(*Call).Position() != (Pos{2, 6}) {
		t.Errorf("binary at %v, call at %v", bin.Position(), bin.X.(*Call).Position())
	}
}

func TestControlFlowParse(t *testing.T) {
	m := mustParse(t, `Функция Ф(Знач Х)
	Пока Х > 0 Цикл
		Если Х = 5 Тогда
			Прервать;
		ИначеЕсли Х = 3 Тогда
			Продолжить;
		ИначеЕсли Х = 2 Тогда
		Иначе
			Х = Х - 1;
		КонецЕсли;
	КонецЦикла;
	Возврат Х;
КонецФункции
`)
	body := m.Routines[0].Body
	if len(body) != 2 {
		t.Fatalf("body = %d statements", len(body))
	}
	w := body[0].(*While)
	if exprString(w.Cond) != "(Х > 0)" || len(w.Body) != 1 {
		t.Fatalf("while: %s, %d", exprString(w.Cond), len(w.Body))
	}
	st := w.Body[0].(*If)
	if len(st.Elifs) != 2 || len(st.Then) != 1 || len(st.Else) != 1 || len(st.Elifs[1].Body) != 0 {
		t.Errorf("if shape: %d elifs, then=%d, else=%d", len(st.Elifs), len(st.Then), len(st.Else))
	}
	if _, ok := st.Then[0].(*Break); !ok {
		t.Errorf("then[0] is %T", st.Then[0])
	}
	if _, ok := st.Elifs[0].Body[0].(*Continue); !ok {
		t.Errorf("elif[0] body is %T", st.Elifs[0].Body[0])
	}
	if st.Position() != (Pos{3, 3}) || w.Position() != (Pos{2, 2}) {
		t.Errorf("positions: if %v, while %v", st.Position(), w.Position())
	}
	// английские слова, регистр не важен, оператор после КонецЕсли без точки с запятой
	mustParse(t, "Function F(Val X)\nWHILE X > 0 DO\nIF X = 1 THEN Break; ELSIF X = 2 THEN Continue; ELSE X = 0; ENDIF\nENDDO;\nReturn X;\nEndFunction")
	// Прервать внутри Если внутри цикла допустимо
	mustParse(t, "Процедура П()\nПока Истина Цикл\nЕсли Истина Тогда\nПрервать;\nКонецЕсли;\nКонецЦикла;\nКонецПроцедуры")
}

func TestForEachAndMemberParse(t *testing.T) {
	m := mustParse(t, `Функция Ф(Строки)
	Перем С;
	Для Каждого Стр Из Строки Цикл
		Если Стр.Количество = 0 Тогда
			Продолжить;
		КонецЕсли;
		С = С + Стр.Количество;
	КонецЦикла;
	Возврат С;
КонецФункции
`)
	fe := m.Routines[0].Body[0].(*ForEach)
	if fe.Var != "Стр" || exprString0(fe.Coll) != "Строки" || len(fe.Body) != 2 || fe.Position() != (Pos{3, 2}) {
		t.Fatalf("foreach: %q %q %d %v", fe.Var, exprString0(fe.Coll), len(fe.Body), fe.Position())
	}
	as := fe.Body[1].(*Assign)
	mem := as.Value.(*Binary).Y.(*Member)
	if mem.Object != "Стр" || mem.Field != "Количество" || mem.Position() != (Pos{7, 11}) {
		t.Errorf("member: %+v at %v", mem, mem.Position())
	}
	mustParse(t, "Procedure P(T)\nFOR EACH R IN T DO\nBreak;\nENDDO;\nEndProcedure")
}

func exprString0(e Expr) string {
	if id, ok := e.(*Ident); ok {
		return id.Name
	}
	return "?"
}
