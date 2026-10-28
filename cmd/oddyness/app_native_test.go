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
)

// Нативный сценарий окна каталога: настоящее окно GTK под Xvfb, ввод — настоящие нажатия xdotool.
// База заранее содержит товар K-1 (создан отдельным процессом). Сценарий: ввести товар с занятым кодом — окно
// остаётся открытым с ошибкой ядра, исправить код, сохранить (окно остаётся), закрыть кнопкой. Затем независимое
// чтение базы: два товара, прежний не тронут. Включается ODDYNESS_NATIVE_UI=1, как TestNativeForm.
func TestNativeApp(t *testing.T) {
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
	if code, _, errs := cli(t, "catalog", "add", configDir, db, "goods", "Код=K-1", "Наименование=Кабель", "ЕдиницаИзмерения=м", "Используется=true", "МинимальныйОстаток=10.500"); code != 0 {
		t.Fatalf("seed: %d %q", code, errs)
	}

	const display = ":93"
	xvfb := exec.CommandContext(ctx, "Xvfb", display, "-screen", "0", "800x900x24", "-nolisten", "tcp")
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

	app := proc(t, ctx, "cli", "app", configDir, db, "goods")
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
		probe := exec.Command("xdotool", "search", "--onlyvisible", "--name", "^oddyness app goods$")
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
	x.shot("app-before")

	// занятый код: «Сохранить» (Tab×6 от поля кода) — окно остаётся открытым
	x.typeText("K-1")
	x.key("Tab")
	x.typeText("Труба")
	x.key("Tab")
	x.typeText("m")
	x.key("Tab", "Tab") // флажок пропускаем
	x.typeText("1")
	x.key("Tab", "Tab", "Return")
	x.shot("app-duplicate")
	if !alive() {
		t.Fatalf("the window closed after saving a duplicate code: %v\nstdout: %s\nstderr: %s", waitErr, out.String(), errb.String())
	}
	// фокус вернулся на код: исправляем K-1 -> K-2 и сохраняем
	x.key("End", "BackSpace")
	x.typeText("2")
	x.key("Tab", "Tab", "Tab", "Tab", "Tab", "Tab", "Return")
	x.shot("app-saved")
	if !alive() {
		t.Fatalf("the window closed after a successful Save: %v\nstdout: %s\nstderr: %s", waitErr, out.String(), errb.String())
	}
	// фокус снова на коде: Tab×7 — «Закрыть»
	x.key("Tab", "Tab", "Tab", "Tab", "Tab", "Tab", "Tab", "Return")

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
	recs, err := s.List(context.Background(), "goods")
	if err != nil || len(recs) != 2 {
		t.Fatalf("records = %v, %v", recs, err)
	}
	if recs[0].Values["goods-code"] != "K-1" || recs[0].Values["goods-title"] != "Кабель" ||
		recs[1].Values["goods-code"] != "K-2" || recs[1].Values["goods-title"] != "Труба" || recs[1].Values["goods-active"] != false {
		t.Errorf("records = %+v", recs)
	}
}
