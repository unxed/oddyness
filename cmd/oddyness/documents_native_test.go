package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

// Нативный сценарий окна поступлений (P16, часть 1): настоящее окно GTK под Xvfb, ввод — настоящие нажатия xdotool.
// База заранее содержит товар K-1 (запись 1). Сценарий: заполнить шапку; «Записать строку» с пустым товаром — окно
// остаётся открытым, ошибка поля, фокус на товаре; ввести товар и записать строку; «Сохранить» (документ записан,
// не проведён); «Провести»; «Закрыть». Затем независимое чтение базы: один документ, проведён, строка 1 / 10.500,
// остаток 10.500. Порядок обхода Tab: поля шапки, поля строки, кнопки (таблицы стоят выше полей и в обход не входят).
// Включается ODDYNESS_NATIVE_UI=1, как TestNativeApp.
func TestNativeDocuments(t *testing.T) {
	if os.Getenv("ODDYNESS_NATIVE_UI") != "1" {
		t.Skip("нативные сценарии: нужен ODDYNESS_NATIVE_UI=1, Xvfb и xdotool (шаг CI)")
	}
	for _, tool := range []string{"Xvfb", "xdotool"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("ODDYNESS_NATIVE_UI=1, но %s не найден: %v", tool, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}
	if code, _, errs := cli(t, "catalog", "add", configDir, db, "goods", "Код=K-1", "Наименование=Кабель", "ЕдиницаИзмерения=м", "Используется=true", "МинимальныйОстаток=0"); code != 0 {
		t.Fatalf("seed: %d %q", code, errs)
	}

	const display = ":94"
	xvfb := exec.CommandContext(ctx, "Xvfb", display, "-screen", "0", "800x1200x24", "-nolisten", "tcp")
	var xvfbLog bytes.Buffer
	xvfb.Stdout, xvfb.Stderr = &xvfbLog, &xvfbLog
	if err := xvfb.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { xvfb.Process.Kill(); xvfb.Wait() }()
	x := xdo{t: t, display: display}
	ready := false
	for i := 0; i < 100 && !ready; i++ {
		probe := exec.Command("xdotool", "getdisplaygeometry")
		probe.Env = append(os.Environ(), "DISPLAY="+display)
		if ready = probe.Run() == nil; !ready {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !ready {
		t.Fatalf("Xvfb did not start: %s", xvfbLog.String())
	}

	app := proc(t, ctx, "cli", "documents", configDir, db, "receipt")
	app.Env = append(app.Env, "DISPLAY="+display, "goWidgets_BACKEND=gtk")
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr = &out, &errb
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- app.Wait() }()
	var waitErr error
	finished := false
	alive := func() bool {
		if finished {
			return false
		}
		select {
		case waitErr = <-exited:
			finished = true
			return false
		default:
			return true
		}
	}
	defer func() {
		if alive() {
			app.Process.Kill()
			<-exited
		}
	}()

	var window string
	for i := 0; i < 300 && window == ""; i++ {
		if !alive() {
			t.Fatalf("the application exited before showing a window: %v\nstdout: %s\nstderr: %s", waitErr, out.String(), errb.String())
		}
		probe := exec.Command("xdotool", "search", "--onlyvisible", "--name", "^oddyness documents receipt$")
		probe.Env = append(os.Environ(), "DISPLAY="+display)
		if b, err := probe.Output(); err == nil {
			window = strings.Fields(string(b) + " ")[0]
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if window == "" {
		t.Fatalf("no window appeared\nstderr: %s", errb.String())
	}
	time.Sleep(500 * time.Millisecond)
	x.run("windowfocus", "--sync", window)
	time.Sleep(300 * time.Millisecond)
	x.shot("documents-before")
	tabs := func(n int) {
		keys := make([]string, n)
		for i := range keys {
			keys[i] = "Tab"
		}
		x.key(keys...)
	}
	mustAlive := func(what string) {
		t.Helper()
		if !alive() {
			t.Fatalf("the window closed %s: %v\nstdout: %s\nstderr: %s", what, waitErr, out.String(), errb.String())
		}
	}

	// шапка: номер, дата, комментарий
	x.typeText("R-1")
	tabs(1)
	x.typeText("2026-10-28")
	tabs(1)
	x.typeText("batch")
	// строка: товар пуст (Tab пропускает его), количество 10.500, «Записать строку»
	tabs(2)
	x.typeText("10.500")
	tabs(1)
	x.key("Return")
	x.shot("documents-line-error")
	mustAlive("after recording a line without a product")
	// фокус вернулся на товар (первое неверное поле): вводим 1 и записываем строку
	x.typeText("1")
	tabs(2)
	x.key("Return")
	x.shot("documents-line-added")
	mustAlive("after recording a line")
	// фокус на поле товара карточки строки: Tab×5 — «Сохранить» (количество, три кнопки строки, «Сохранить»)
	tabs(5)
	x.key("Return")
	x.shot("documents-saved")
	mustAlive("after Save")
	// фокус вернулся на номер: Tab×9 — «Провести» (дата, комментарий, товар, количество, три кнопки строки, «Сохранить», «Провести»)
	tabs(9)
	x.key("Return")
	x.shot("documents-posted")
	mustAlive("after Post")
	// фокус снова на номере: Tab×12 — «Закрыть» (девять до «Провести», затем «Отменить проведение», «Новый документ», «Закрыть»)
	tabs(12)
	x.key("Return")

	deadline := time.After(15 * time.Second)
	for alive() {
		select {
		case <-deadline:
			t.Fatalf("the application did not exit after Close\nstdout: %s\nstderr: %s", out.String(), errb.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if waitErr != nil || out.String() != "" {
		t.Fatalf("exit error=%v stdout=%q stderr=%q", waitErr, out.String(), errb.String())
	}

	model, err := metadata.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(db, model)
	if err != nil {
		t.Fatalf("database is still locked or broken: %v", err)
	}
	defer s.Close()
	docs, err := s.ListDocuments(context.Background(), "receipt")
	if err != nil || len(docs) != 1 {
		t.Fatalf("documents = %+v, %v", docs, err)
	}
	d := docs[0]
	lines := d.Tables["receipt-items"]
	if !d.Posted || d.Header["receipt-number"] != "R-1" || d.Header["receipt-date"] != "2026-10-28" || d.Header["receipt-comment"] != "batch" ||
		len(lines) != 1 || lines[0].Values["receipt-product"] != (sqlite.Ref{Catalog: "goods", ID: 1}) || lines[0].Values["receipt-qty"].(value.Number).String() != "10.500" {
		t.Errorf("document = %+v", d)
	}
	bs, err := s.Balances(context.Background(), "stock", sqlite.BalanceQuery{})
	if err != nil || len(bs) != 1 || bs[0].Resources["stock-qty"].String() != "10.500" {
		t.Errorf("balances = %+v, %v", bs, err)
	}
}
