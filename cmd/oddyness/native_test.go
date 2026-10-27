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

// Нативные сценарии: настоящее окно GTK под Xvfb, ввод — настоящие нажатия клавиш
// через xdotool (XTEST). Тест включается переменной ODDYNESS_NATIVE_UI=1 (её ставит
// шаг CI); с ней отсутствие Xvfb, xdotool или GTK — ошибка, а не пропуск.
// Каждый сценарий — отдельные процессы приложения со своим дисплеем Xvfb и своей
// базой; сценарии идут параллельно.

type xdo struct {
	t       *testing.T
	display string
}

func (x xdo) run(args ...string) string {
	x.t.Helper()
	cmd := exec.Command("xdotool", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+x.display)
	out, err := cmd.CombinedOutput()
	if err != nil {
		x.t.Fatalf("xdotool %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (x xdo) key(keys ...string) {
	x.t.Helper()
	x.run(append([]string{"key", "--delay", "40"}, keys...)...)
	time.Sleep(150 * time.Millisecond)
}

func (x xdo) typeText(text string) {
	x.t.Helper()
	x.run("type", "--delay", "40", text)
	time.Sleep(150 * time.Millisecond)
}

// scenario выполняет ввод в уже показанном окне.
type scenario func(x xdo, window string, alive func() bool)

func runNative(t *testing.T, display int, name string, script scenario, check func(t *testing.T, rec sqlite.Record)) {
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
	dir := t.TempDir()
	db := filepath.Join(dir, "db.sqlite")
	if code, _, errs := cli(t, "db", "create", configDir, db); code != 0 {
		t.Fatalf("db create: %d %q", code, errs)
	}

	disp := fmt.Sprintf(":%d", display)
	xvfb := exec.CommandContext(ctx, "Xvfb", disp, "-screen", "0", "800x700x24", "-nolisten", "tcp")
	var xvfbLog bytes.Buffer
	xvfb.Stdout, xvfb.Stderr = &xvfbLog, &xvfbLog
	if err := xvfb.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { xvfb.Process.Kill(); xvfb.Wait() }()
	x := xdo{t: t, display: disp}
	ready := false
	for i := 0; i < 100 && !ready; i++ {
		probe := exec.Command("xdotool", "getdisplaygeometry")
		probe.Env = append(os.Environ(), "DISPLAY="+disp)
		ready = probe.Run() == nil
		if !ready {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !ready {
		t.Fatalf("Xvfb %s did not start: %s", disp, xvfbLog.String())
	}

	app := proc(t, ctx, "cli", "form", configDir, db, "goods")
	app.Env = append(app.Env, "DISPLAY="+disp, "goWidgets_BACKEND=gtk")
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr = &out, &errb
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- app.Wait() }()
	var waitErr error
	done := false
	alive := func() bool {
		if done {
			return false
		}
		select {
		case waitErr = <-exited:
			done = true
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
			t.Fatalf("%s: the application exited before showing a window: %v\nstdout: %s\nstderr: %s", name, waitErr, out.String(), errb.String())
		}
		probe := exec.Command("xdotool", "search", "--onlyvisible", "--name", "^oddyness goods$")
		probe.Env = append(os.Environ(), "DISPLAY="+disp)
		if b, err := probe.Output(); err == nil {
			window = strings.Fields(string(b) + " ")[0]
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if window == "" {
		t.Fatalf("%s: no window appeared\nstderr: %s", name, errb.String())
	}
	time.Sleep(500 * time.Millisecond)
	x.run("windowfocus", "--sync", window)
	time.Sleep(300 * time.Millisecond)

	script(x, window, alive)

	deadline := time.After(30 * time.Second)
	for alive() {
		select {
		case <-deadline:
			t.Fatalf("%s: the application did not exit after saving\nstdout: %s\nstderr: %s", name, out.String(), errb.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if waitErr != nil || out.String() != "id 1\n" {
		t.Fatalf("%s: exit error=%v stdout=%q stderr=%q", name, waitErr, out.String(), errb.String())
	}

	// проверяем сохранённое независимым чтением базы после выхода процесса
	model, err := metadata.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(db, model)
	if err != nil {
		t.Fatalf("%s: database is still locked or broken: %v", name, err)
	}
	defer s.Close()
	recs, err := s.List(ctx, "goods")
	if err != nil || len(recs) != 1 {
		t.Fatalf("%s: records = %v, %v", name, recs, err)
	}
	check(t, recs[0])
}

func TestNativeForm(t *testing.T) {
	// Порядок Tab: код, наименование, единица, флажок, мин. остаток, аналог, «Сохранить», «Закрыть».
	t.Run("fill and save", func(t *testing.T) {
		t.Parallel()
		runNative(t, 91, "fill and save", func(x xdo, _ string, alive func() bool) {
			x.typeText("K-1")
			x.key("Tab")
			x.typeText("Cable")
			x.key("Tab")
			x.typeText("m")
			x.key("Tab")
			x.key("space") // флажок «Используется»
			x.key("Tab")
			x.typeText("10.500")
			x.key("Tab", "Tab") // аналог пропускаем, фокус на «Сохранить»
			x.key("Return")
		}, func(t *testing.T, rec sqlite.Record) {
			n := rec.Values["goods-min"].(value.Number)
			if rec.Values["goods-code"] != "K-1" || rec.Values["goods-title"] != "Cable" || rec.Values["goods-unit"] != "m" ||
				rec.Values["goods-active"] != true || n.String() != "10.500" || rec.Values["goods-analog"] != nil {
				t.Errorf("record = %+v", rec.Values)
			}
		})
	})
	t.Run("invalid then fix", func(t *testing.T) {
		t.Parallel()
		runNative(t, 92, "invalid then fix", func(x xdo, _ string, alive func() bool) {
			// пустая форма: Tab до «Сохранить» и Return — форма остаётся открытой, фокус на первом поле
			x.key("Tab", "Tab", "Tab", "Tab", "Tab", "Tab")
			x.key("Return")
			if !alive() {
				x.t.Error("the form closed after an invalid Save")
				return
			}
			x.typeText("K-2")
			x.key("Tab")
			x.typeText("Cable2")
			x.key("Tab")
			x.typeText("m")
			x.key("Tab", "Tab") // флажок оставляем снятым
			x.typeText("1")
			x.key("Tab", "Tab")
			x.key("Return")
		}, func(t *testing.T, rec sqlite.Record) {
			n := rec.Values["goods-min"].(value.Number)
			if rec.Values["goods-code"] != "K-2" || rec.Values["goods-title"] != "Cable2" || rec.Values["goods-unit"] != "m" ||
				rec.Values["goods-active"] != false || n.String() != "1" {
				t.Errorf("record = %+v", rec.Values)
			}
		})
	})
}
