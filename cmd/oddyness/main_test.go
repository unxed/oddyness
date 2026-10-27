package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"version"}, &stdout, &stderr); got != 0 {
		t.Fatalf("run(version) exit = %d, want 0", got)
	}
	if got, want := stdout.String(), "oddyness dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"nope"}, &stdout, &stderr); got == 0 {
		t.Fatal("run(unknown command) exit = 0, want non-zero")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); !strings.Contains(got, `unknown command "nope"`) {
		t.Fatalf("stderr = %q, want clear unknown-command error", got)
	}
}

func TestRunWithoutCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(nil, &stdout, &stderr); got == 0 {
		t.Fatal("run(no arguments) exit = 0, want non-zero")
	}
	if got := stderr.String(); !strings.Contains(got, "usage: oddyness <command>") {
		t.Fatalf("stderr = %q, want usage", got)
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBSLCheckOK(t *testing.T) {
	src := "Функция Сумма(А, Б) Экспорт\n\tВозврат А + Б;\nКонецФункции\n\nПроцедура П()\nКонецПроцедуры\n"
	// файл с BOM, как их пишет конфигуратор
	for name, content := range map[string]string{"plain.bsl": src, "bom.bsl": "\uFEFF" + src} {
		path := writeFile(t, name, content)
		var stdout, stderr bytes.Buffer
		if got := run([]string{"bsl", "check", path}, &stdout, &stderr); got != 0 {
			t.Fatalf("%s: exit = %d, stderr = %q", name, got, stderr.String())
		}
		want := path + ": ok, 1 procedures, 1 functions, 0 module variable declarations\n"
		if stdout.String() != want || stderr.String() != "" {
			t.Errorf("%s: stdout = %q, stderr = %q, want stdout %q", name, stdout.String(), stderr.String(), want)
		}
	}
}

func TestBSLCheckDiagnostics(t *testing.T) {
	path := writeFile(t, "bad.bsl", "Процедура П()\n  Попытка\nКонецПроцедуры\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"bsl", "check", path}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d, want 1", got)
	}
	want := path + `:2:3: "Попытка" statement is not supported` + "\n"
	if stderr.String() != want || stdout.String() != "" {
		t.Errorf("stderr = %q, stdout = %q, want stderr %q", stderr.String(), stdout.String(), want)
	}
}

func TestBSLCheckErrors(t *testing.T) {
	bad := writeFile(t, "latin1.bsl", "Процедура П()\n\xff\nКонецПроцедуры\n")
	for name, args := range map[string][]string{
		"no file":      {"bsl", "check"},
		"extra":        {"bsl", "check", "a", "b"},
		"wrong verb":   {"bsl", "run", "a"},
		"no subcmd":    {"bsl"},
		"missing file": {"bsl", "check", filepath.Join(t.TempDir(), "absent.bsl")},
		"bad utf8":     {"bsl", "check", bad},
	} {
		var stdout, stderr bytes.Buffer
		got := run(args, &stdout, &stderr)
		wantCode := 2
		if name == "bad utf8" {
			wantCode = 1
		}
		if got != wantCode || stdout.String() != "" || stderr.Len() == 0 {
			t.Errorf("%s: exit = %d, stdout = %q, stderr = %q", name, got, stdout.String(), stderr.String())
		}
	}
}

func TestBSLRun(t *testing.T) {
	good := writeFile(t, "good.bsl", "Функция Сумма(Знач А, Знач Б)\n\tВозврат А + Б;\nКонецФункции\n\nФункция Неверно()\n\tВозврат 1 + \"а\";\nКонецФункции\n\nФункция Привет(Имя)\n\tВозврат \"Привет, \" + Имя;\nКонецФункции\n\nФункция Доля(А, Б)\n\tВозврат А / Б;\nКонецФункции\n\nФункция Стоимость(Знач Кол, Знач Цена)\n\tВозврат Кол * Цена;\nКонецФункции\n")
	bad := writeFile(t, "bad.bsl", "Функция Ф()\n\tВозврат Икс;\nКонецФункции\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"bsl", "run", bad, "Ф"}, &stdout, &stderr); got != 1 || stdout.String() != "" ||
		stderr.String() != bad+": compile error: 2:10: unknown name \"Икс\"\n" {
		t.Errorf("compile error: exit=%d stdout=%q stderr=%q", got, stdout.String(), stderr.String())
	}
	for _, c := range []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"Сумма", "10.500", "2.250"}, 0, "12.750\n", ""},
		{[]string{"сумма", "0.1", "0.2"}, 0, "0.3\n", ""},
		{[]string{"Привет", "мир"}, 0, "Привет, мир\n", ""},
		{[]string{"Стоимость", "2.250", "4"}, 0, "9.000\n", ""},
		{[]string{"Доля", "1", "4"}, 0, "0.25\n", ""},
		{[]string{"Доля", "1", "3"}, 1, "", good + ": runtime error: 14:12: value: result is not exactly representable\n"},
		{[]string{"Доля", "1", "0"}, 1, "", good + ": runtime error: 14:12: value: division by zero\n"},
		{[]string{"Неверно"}, 1, "", good + ": runtime error: 6:12: cannot add Number and String\n"},
		{[]string{"Нет"}, 1, "", good + ": runtime error: unknown routine \"Нет\"\n"},
		{[]string{"Сумма", "1"}, 1, "", good + ": runtime error: \"Сумма\" takes 2 arguments, got 1\n"},
	} {
		stdout.Reset()
		stderr.Reset()
		if got := run(append([]string{"bsl", "run", good}, c.args...), &stdout, &stderr); got != c.code || stdout.String() != c.stdout || stderr.String() != c.stderr {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q; want exit=%d stdout=%q stderr=%q", c.args, got, stdout.String(), stderr.String(), c.code, c.stdout, c.stderr)
		}
	}
}
