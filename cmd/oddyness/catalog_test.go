package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
)

// TestMain позволяет тестам запускать этот же бинарник отдельными процессами:
// роль cli — обычная команда, роль hold — открыть базу и держать сессию, пока
// не закроется stdin.
func TestMain(m *testing.M) {
	switch os.Getenv("ODDYNESS_TEST_ROLE") {
	case "cli":
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	case "hold":
		model, err := metadata.Load(os.Args[1])
		if err != nil {
			os.Exit(3)
		}
		s, err := sqlite.Open(os.Args[2], model)
		if err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(4)
		}
		os.Stdout.WriteString("locked\n")
		io.Copy(io.Discard, os.Stdin)
		s.Close()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var configDir = filepath.Join("..", "..", "testdata", "configs", "small-stock")

func cli(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

const addCable = "Код=K-1 Наименование=Кабель ЕдиницаИзмерения=м Используется=true МинимальныйОстаток=10.500"

func TestCatalogCommands(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, out, errs := cli(t, "db", "create", configDir, db); code != 0 || out != db+": created\n" || errs != "" {
		t.Fatalf("db create: %d %q %q", code, out, errs)
	}
	add := append([]string{"catalog", "add", configDir, db, "Товары"}, strings.Fields(addCable)...)
	if code, out, errs := cli(t, add...); code != 0 || out != "id 1\n" || errs != "" {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	line := `1: Код="K-1" Наименование="Кабель" ЕдиницаИзмерения="м" Используется=true МинимальныйОстаток=10.500 Аналог=-` + "\n"
	for _, args := range [][]string{
		{"catalog", "get", configDir, db, "goods", "1"},
		{"catalog", "list", configDir, db, "товары"},
	} {
		if code, out, errs := cli(t, args...); code != 0 || out != line || errs != "" {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	// поля по идентификатору, ссылка на первую запись
	if code, out, errs := cli(t, "catalog", "add", configDir, db, "goods", "goods-code=K-2", "goods-title=Шнур", "goods-unit=м",
		"goods-active=false", "goods-min=0.5", "goods-analog=1"); code != 0 || out != "id 2\n" || errs != "" {
		t.Fatalf("add 2: %d %q %q", code, out, errs)
	}
	want2 := `2: Код="K-2" Наименование="Шнур" ЕдиницаИзмерения="м" Используется=false МинимальныйОстаток=0.5 Аналог=1` + "\n"
	if code, out, _ := cli(t, "catalog", "list", configDir, db, "goods"); code != 0 || out != line+want2 {
		t.Errorf("list: %d %q", code, out)
	}
}

func TestCatalogErrors(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, _ := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatal("db create failed")
	}
	cable := append([]string{"catalog", "add", configDir, db, "goods"}, strings.Fields(addCable)...)
	if code, _, errs := cli(t, cable...); code != 0 {
		t.Fatalf("seed: %d %q", code, errs)
	}
	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"duplicate code", cable, 1, "duplicate value of a unique field"},
		{"unknown field", []string{"catalog", "add", configDir, db, "goods", "Цвет=красный"}, 1, `has no field "Цвет"`},
		{"missing fields", []string{"catalog", "add", configDir, db, "goods", "Код=K-3"}, 1, "missing value for field"},
		{"bad assignment", []string{"catalog", "add", configDir, db, "goods", "Код"}, 1, "want field=value"},
		{"set twice", []string{"catalog", "add", configDir, db, "goods", "Код=a", "код=b"}, 1, "set twice"},
		{"bad number", []string{"catalog", "add", configDir, db, "goods", "МинимальныйОстаток=1.2.3"}, 1, "invalid number"},
		{"bad bool", []string{"catalog", "add", configDir, db, "goods", "Используется=да"}, 1, "invalid boolean"},
		{"bad ref", []string{"catalog", "add", configDir, db, "goods", "Аналог=x"}, 1, "invalid reference"},
		{"unknown catalog", []string{"catalog", "list", configDir, db, "Нет"}, 1, `unknown catalog "Нет"`},
		{"unknown id", []string{"catalog", "get", configDir, db, "goods", "99"}, 1, "record not found"},
		{"bad id", []string{"catalog", "get", configDir, db, "goods", "x"}, 1, `invalid id "x"`},
		{"db exists", []string{"db", "create", configDir, db}, 1, "already exists"},
		{"missing db", []string{"catalog", "list", configDir, filepath.Join(t.TempDir(), "absent"), "goods"}, 1, "no such file"},
		{"bad config", []string{"catalog", "list", t.TempDir(), db, "goods"}, 1, "metadata.json"},
		{"usage db", []string{"db"}, 2, "usage:"},
		{"usage db verb", []string{"db", "drop", configDir, db}, 2, "usage:"},
		{"usage catalog", []string{"catalog"}, 2, "usage:"},
		{"usage catalog verb", []string{"catalog", "drop", configDir, db, "goods"}, 2, "usage:"},
		{"usage get arity", []string{"catalog", "get", configDir, db, "goods"}, 2, "usage:"},
		{"usage list arity", []string{"catalog", "list", configDir, db, "goods", "extra"}, 2, "usage:"},
	}
	for _, c := range cases {
		code, out, errs := cli(t, c.args...)
		if code != c.code || out != "" || !strings.Contains(errs, c.msg) {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q; want exit %d containing %q", c.name, code, out, errs, c.code, c.msg)
		}
	}
	// после всех отказов в базе по-прежнему одна запись
	if _, out, _ := cli(t, "catalog", "list", configDir, db, "goods"); strings.Count(out, "\n") != 1 {
		t.Errorf("list after errors: %q", out)
	}
}

// proc запускает этот же бинарник отдельным процессом с ролью role.
func proc(t *testing.T, ctx context.Context, role string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), "ODDYNESS_TEST_ROLE="+role)
	return cmd
}

func runProc(t *testing.T, ctx context.Context, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := proc(t, ctx, "cli", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

// Межпроцессная проверка: один процесс создаёт товар, другой его читает; пока
// третий процесс держит сессию, команды получают отказ, после его выхода работают.
func TestCrossProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := runProc(t, ctx, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}
	add := append([]string{"catalog", "add", configDir, db, "goods"}, strings.Fields(addCable)...)
	if code, out, errs := runProc(t, ctx, add...); code != 0 || out != "id 1\n" {
		t.Fatalf("add in process 1: %d %q %q", code, out, errs)
	}
	line := `1: Код="K-1" Наименование="Кабель" ЕдиницаИзмерения="м" Используется=true МинимальныйОстаток=10.500 Аналог=-` + "\n"
	if code, out, errs := runProc(t, ctx, "catalog", "list", configDir, db, "goods"); code != 0 || out != line {
		t.Fatalf("list in process 2: %d %q %q", code, out, errs)
	}

	// процесс-владелец сессии
	holder := proc(t, ctx, "hold", configDir, db)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if got, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || got != "locked\n" {
		t.Fatalf("holder did not take the session: %q, %v", got, err)
	}
	second := append([]string{"catalog", "add", configDir, db, "goods"}, "Код=K-2", "Наименование=Шнур", "ЕдиницаИзмерения=м", "Используется=true", "МинимальныйОстаток=1")
	code, out, errs := runProc(t, ctx, second...)
	if code != 1 || out != "" || !strings.Contains(errs, "locked by another session") {
		t.Errorf("add while the session is held: %d %q %q", code, out, errs)
	}
	if code, _, errs := runProc(t, ctx, "catalog", "list", configDir, db, "goods"); code != 1 || !strings.Contains(errs, "locked by another session") {
		t.Errorf("list while the session is held: %d %q", code, errs)
	}
	stdin.Close()
	if err := holder.Wait(); err != nil {
		t.Fatalf("holder: %v", err)
	}
	if code, out, errs := runProc(t, ctx, second...); code != 0 || out != "id 2\n" {
		t.Errorf("add after the session ended: %d %q %q", code, out, errs)
	}
	if code, out, _ := runProc(t, ctx, "catalog", "list", configDir, db, "goods"); code != 0 || strings.Count(out, "\n") != 2 || !strings.HasPrefix(out, line) {
		t.Errorf("final list: %d %q", code, out)
	}
}

func seedProducts(t *testing.T, db string) {
	t.Helper()
	for _, a := range [][]string{
		{"Код=A", "Наименование=Гайка", "ЕдиницаИзмерения=шт", "Используется=true", "МинимальныйОстаток=0"},
		{"Код=B", "Наименование=Болт", "ЕдиницаИзмерения=шт", "Используется=true", "МинимальныйОстаток=0"},
	} {
		args := append([]string{"catalog", "add", configDir, db, "goods"}, a...)
		if code, _, errs := cli(t, args...); code != 0 {
			t.Fatalf("seed: %d %q", code, errs)
		}
	}
}

func TestDocumentCommands(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}
	seedProducts(t, db)
	add := []string{"document", "add", configDir, db, "Поступление", "Номер=R-1", "Дата=2026-10-27", "Комментарий=тест",
		"--row", "Товары", "Товар=1", "Количество=10.500", "--row", "Товары", "Товар=2", "Количество=2.250"}
	if code, out, errs := cli(t, add...); code != 0 || out != "id 1\n" || errs != "" {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	want := "1: Номер=\"R-1\" Дата=\"2026-10-27\" Комментарий=\"тест\"\n" +
		"  Товары[1]: Товар=1 Количество=10.500\n" +
		"  Товары[2]: Товар=2 Количество=2.250\n"
	for _, args := range [][]string{
		{"document", "get", configDir, db, "receipt", "1"},
		{"document", "list", configDir, db, "поступление"},
	} {
		if code, out, errs := cli(t, args...); code != 0 || out != want || errs != "" {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	// другой вид документа: тот же номер допустим, строк нет
	if code, out, _ := cli(t, "document", "add", configDir, db, "issue", "Номер=R-1", "Дата=2026-10-28", "Получатель=цех"); code != 0 || out != "id 1\n" {
		t.Errorf("issue: %d %q", code, out)
	}
}

func TestDocumentErrors(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, _ := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatal("db create failed")
	}
	seedProducts(t, db)
	head := []string{"document", "add", configDir, db, "receipt", "Номер=R-1", "Дата=2026-10-27", "Комментарий=x"}
	row := func(product, qty string) []string {
		return []string{"--row", "receipt-items", "Товар=" + product, "Количество=" + qty}
	}
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	if code, _, errs := cli(t, join(head, row("1", "1"))...); code != 0 {
		t.Fatalf("seed document: %d %q", code, errs)
	}
	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"duplicate number", join(head, row("1", "5")), 1, "duplicate value of a unique field"},
		{"too precise second row", join([]string{"document", "add", configDir, db, "receipt", "Номер=R-2", "Дата=2026-10-27", "Комментарий=x"}, row("1", "1"), row("2", "0.0001")), 1, "fraction digits"},
		{"zero quantity", join([]string{"document", "add", configDir, db, "receipt", "Номер=R-3", "Дата=2026-10-27", "Комментарий=x"}, row("1", "0")), 1, "greater than zero"},
		{"missing product", join([]string{"document", "add", configDir, db, "receipt", "Номер=R-4", "Дата=2026-10-27", "Комментарий=x", "--row", "receipt-items", "Количество=1"}), 1, "required reference"},
		{"missing product record", join([]string{"document", "add", configDir, db, "receipt", "Номер=R-5", "Дата=2026-10-27", "Комментарий=x"}, row("1", "1"), row("999", "1")), 1, "missing record"},
		{"bad date", join([]string{"document", "add", configDir, db, "receipt", "Номер=R-6", "Дата=27.10.2026", "Комментарий=x"}), 1, "not a date"},
		{"unknown table", join(head, []string{"--row", "nope", "Товар=1"}), 1, `has no table "nope"`},
		{"row without table", join(head, []string{"--row"}), 1, "--row needs a table name"},
		{"unknown document", []string{"document", "list", configDir, db, "нет"}, 1, `unknown document "нет"`},
		{"unknown id", []string{"document", "get", configDir, db, "receipt", "99"}, 1, "record not found"},
		{"usage", []string{"document"}, 2, "usage:"},
		{"usage verb", []string{"document", "drop", configDir, db, "receipt"}, 2, "usage:"},
		{"usage get arity", []string{"document", "get", configDir, db, "receipt"}, 2, "usage:"},
	}
	for _, c := range cases {
		code, out, errs := cli(t, c.args...)
		if code != c.code || out != "" || !strings.Contains(errs, c.msg) {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q; want exit %d containing %q", c.name, code, out, errs, c.code, c.msg)
		}
	}
	// после всех отказов в базе один документ с одной строкой
	_, out, _ := cli(t, "document", "list", configDir, db, "receipt")
	if strings.Count(out, "\n") != 2 || !strings.HasPrefix(out, "1: Номер=\"R-1\"") {
		t.Errorf("list after errors: %q", out)
	}
}
