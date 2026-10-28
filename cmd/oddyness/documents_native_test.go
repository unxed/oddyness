package main

import (
	"bytes"
	"context"
	"fmt"
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

// nativeDocs — окно `oddyness documents` в отдельном процессе под своим дисплеем Xvfb; ввод — настоящие нажатия xdotool.
type nativeDocs struct {
	x         xdo
	tabs      func(n int)
	mustAlive func(what string)
}

// runNativeDocuments поднимает Xvfb на display, запускает окно документов вида document над базой db, выполняет script
// и проверяет чистое завершение процесса после script (скрипт заканчивается «Закрыть»). Базу после этого читает вызывающий.
func runNativeDocuments(t *testing.T, display, document, db string, script func(w nativeDocs)) {
	t.Helper()
	for _, tool := range []string{"Xvfb", "xdotool"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("ODDYNESS_NATIVE_UI=1, но %s не найден: %v", tool, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
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

	app := proc(t, ctx, "cli", "documents", configDir, db, document)
	app.Env = append(app.Env, "DISPLAY="+display, "goWidgets_BACKEND=gtk", "ODDYNESS_UI_TRACE=1")
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
	exitInfo := func() string {
		return fmt.Sprintf("%v\nstdout: %s\nstderr: %s", waitErr, out.String(), errb.String())
	}

	var window string
	title := "^oddyness documents " + document + "$"
	for i := 0; i < 300 && window == ""; i++ {
		if !alive() {
			t.Fatalf("the application exited before showing a window: %s", exitInfo())
		}
		probe := exec.Command("xdotool", "search", "--onlyvisible", "--name", title)
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
	w := nativeDocs{x: x}
	w.tabs = func(n int) {
		keys := make([]string, n)
		for i := range keys {
			keys[i] = "Tab"
		}
		x.key(keys...)
	}
	w.mustAlive = func(what string) {
		t.Helper()
		if !alive() {
			t.Fatalf("the window closed %s: %s", what, exitInfo())
		}
	}
	script(w)

	deadline := time.After(15 * time.Second)
	for alive() {
		select {
		case <-deadline:
			x.shot("not-exited-" + document)
			t.Fatalf("the application did not exit after Close\nstdout: %s\nstderr: %s", out.String(), errb.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if waitErr != nil || out.String() != "" {
		t.Fatalf("exit error=%v stdout=%q stderr=%q", waitErr, out.String(), errb.String())
	}
	t.Logf("trace:\n%s", errb.String())
}

// openStore открывает базу после выхода процесса (ошибка — база ещё занята или повреждена).
func openStore(t *testing.T, db string) *sqlite.Store {
	t.Helper()
	model, err := metadata.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(db, model)
	if err != nil {
		t.Fatalf("database is still locked or broken: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func balanceOf(t *testing.T, s *sqlite.Store, product int64) string {
	t.Helper()
	bs, err := s.Balances(context.Background(), "stock", sqlite.BalanceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.Dimensions["stock-product"] == (sqlite.Ref{Catalog: "goods", ID: product}) {
			return b.Resources["stock-qty"].String()
		}
	}
	return "0"
}

func newBase(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}
	if code, _, errs := cli(t, "catalog", "add", configDir, db, "goods", "Код=K-1", "Наименование=Кабель", "ЕдиницаИзмерения=м", "Используется=true", "МинимальныйОстаток=0"); code != 0 {
		t.Fatalf("seed: %d %q", code, errs)
	}
	return db
}

// Нативный сценарий окна поступлений (P16, часть 1): настоящее окно GTK под Xvfb, ввод — настоящие нажатия xdotool.
// База заранее содержит товар K-1 (запись 1). Сценарий: заполнить шапку; «Записать строку» с пустым товаром — окно
// остаётся открытым, ошибка поля, фокус на товаре; ввести товар и записать строку; «Сохранить» (документ записан,
// не проведён); «Провести»; «Закрыть». Затем независимое чтение базы: один документ, проведён, строка 1 / 10.500,
// остаток 10.500. Порядок обхода Tab: поля шапки, поля строки, кнопки (таблицы стоят выше полей и в обход не входят).
// Включается ODDYNESS_NATIVE_UI=1, как TestNativeApp; идёт параллельно с TestNativeIssue (свой дисплей и своя база).
func TestNativeDocuments(t *testing.T) {
	if os.Getenv("ODDYNESS_NATIVE_UI") != "1" {
		t.Skip("нативные сценарии: нужен ODDYNESS_NATIVE_UI=1, Xvfb и xdotool (шаг CI)")
	}
	t.Parallel()
	db := newBase(t)
	runNativeDocuments(t, ":94", "receipt", db, func(w nativeDocs) {
		x, tabs := w.x, w.tabs
		x.shot("documents-before")
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
		w.mustAlive("after recording a line without a product")
		// фокус вернулся на товар (первое неверное поле): вводим 1 и записываем строку
		x.typeText("1")
		tabs(2)
		x.key("Return")
		x.shot("documents-line-added")
		w.mustAlive("after recording a line")
		// фокус на поле товара карточки строки: Tab×5 — «Сохранить» (количество, три кнопки строки, «Сохранить»)
		tabs(5)
		x.key("Return")
		x.shot("documents-saved")
		w.mustAlive("after Save")
		// фокус вернулся на номер: Tab×9 — «Провести» (дата, комментарий, товар, количество, три кнопки строки, «Сохранить», «Провести»)
		tabs(9)
		x.key("Return")
		x.shot("documents-posted")
		w.mustAlive("after Post")
		// фокус снова на номере: Tab×12 — «Закрыть» (девять до «Провести», затем «Отменить проведение», «Новый документ», «Закрыть»)
		tabs(12)
		x.key("Return")
	})

	s := openStore(t, db)
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
	if got := balanceOf(t, s, 1); got != "10.500" {
		t.Errorf("balance = %s", got)
	}
}

// Нативный сценарий окна выдач (P16, часть 2): S10, S03 и S12 через настоящее окно. База заранее содержит товар K-1
// и проведённое поступление 8.250 (команды CLI). Сценарий: выдача I-1 из двух строк 5.000 + 4.000 — «Провести»
// отказано целиком, окно открыто, документ записан, но не проведён (S10); «Новый документ»; выдача I-2 на 2.250 проведена
// (S03); правка получателя проведённого документа и «Сохранить» — отказ ядра, документ прежний (S12); «Закрыть».
// Затем независимое чтение базы. Идёт параллельно с TestNativeDocuments.
func TestNativeIssue(t *testing.T) {
	if os.Getenv("ODDYNESS_NATIVE_UI") != "1" {
		t.Skip("нативные сценарии: нужен ODDYNESS_NATIVE_UI=1, Xvfb и xdotool (шаг CI)")
	}
	t.Parallel()
	db := newBase(t)
	if code, _, errs := cli(t, "document", "add", configDir, db, "receipt", "Номер=R-1", "Дата=2026-10-28", "Комментарий=x", "--row", "receipt-items", "Товар=1", "Количество=8.250"); code != 0 {
		t.Fatalf("seed receipt: %d %q", code, errs)
	}
	if code, _, errs := cli(t, "document", "post", configDir, db, "receipt", "1"); code != 0 {
		t.Fatalf("post receipt: %d %q", code, errs)
	}
	runNativeDocuments(t, ":95", "issue", db, func(w nativeDocs) {
		x, tabs := w.x, w.tabs
		x.shot("issue-before")
		// S10: две строки одного товара 5.000 + 4.000 при остатке 8.250
		x.typeText("I-1")
		tabs(1)
		x.typeText("2026-10-28")
		tabs(1)
		x.typeText("shop")
		tabs(1) // товар
		x.typeText("1")
		tabs(1)
		x.typeText("5.000")
		tabs(1)
		x.key("Return") // «Записать строку»; фокус вернулся на товар
		x.typeText("1")
		tabs(1)
		x.typeText("4.000")
		tabs(1)
		x.key("Return")
		w.mustAlive("after recording the second line")
		// фокус на товаре: «Провести» — после количества, трёх кнопок строки и «Сохранить»
		tabs(6)
		x.key("Return")
		x.shot("issue-rejected")
		w.mustAlive("after a rejected Post")
		// фокус на номере: «Новый документ» — Tab×11 (дата, получатель, товар, количество, три кнопки строки, «Сохранить», «Провести», «Отменить проведение», «Новый документ»)
		tabs(11)
		x.key("Return")
		w.mustAlive("after New")
		// S03: выдача I-2 на 2.250
		x.typeText("I-2")
		tabs(1)
		x.typeText("2026-10-28")
		tabs(1)
		x.typeText("shop")
		tabs(1)
		x.typeText("1")
		tabs(1)
		x.typeText("2.250")
		tabs(1)
		x.key("Return")
		tabs(6)
		x.key("Return")
		x.shot("issue-posted")
		w.mustAlive("after Post of a valid issue")
		// S12: получатель проведённого документа (Tab×2 от номера), «Сохранить» (получатель, товар, количество, три кнопки строки, «Сохранить»)
		tabs(2)
		x.typeText("other")
		tabs(6)
		x.key("Return")
		time.Sleep(time.Second)
		x.shot("issue-edit-posted")
		w.mustAlive("after saving a posted document")
		// фокус на номере: «Закрыть» — Tab×12
		tabs(12)
		x.key("Return")
	})

	s := openStore(t, db)
	docs, err := s.ListDocuments(context.Background(), "issue")
	if err != nil || len(docs) != 2 {
		t.Fatalf("issues = %+v, %v", docs, err)
	}
	first, second := docs[0], docs[1]
	qty := func(d sqlite.Document, i int) string {
		return d.Tables["issue-items"][i].Values["issue-qty"].(value.Number).String()
	}
	if first.Posted || first.Header["issue-number"] != "I-1" || len(first.Tables["issue-items"]) != 2 || qty(first, 0) != "5.000" || qty(first, 1) != "4.000" {
		t.Errorf("S10: issue I-1 = %+v", first)
	}
	if !second.Posted || second.Header["issue-number"] != "I-2" || second.Header["issue-recipient"] != "shop" || len(second.Tables["issue-items"]) != 1 || qty(second, 0) != "2.250" {
		t.Errorf("S03/S12: issue I-2 = %+v", second)
	}
	if got := balanceOf(t, s, 1); got != "6.000" {
		t.Errorf("balance = %s, want 6.000", got)
	}
}
