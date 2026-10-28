package main

import (
	"fmt"
	"io"
	"os"

	"github.com/unxed/goWidgets"

	"github.com/unxed/oddyness/internal/formsbackend"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/report"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/ui"
)

// runReport выполняет `oddyness report <config-dir> <db-file> <report>`: окно отчёта (id или имя из метаданных) с
// полями параметров, кнопкой «Сформировать» и таблицей результата. Окно закрывается кнопкой «Закрыть» или крестиком;
// сессия базы занята на время работы окна.
func runReport(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "usage: oddyness report <config-dir> <db-file> <report>")
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	m, err := metadata.Load(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	rep, err := report.Load(m, args[2])
	if err != nil {
		return fail(err)
	}
	s, err := sqlite.Open(args[1], m)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	if os.Getenv("ODDYNESS_UI_TRACE") == "1" {
		ui.Trace = func(line string) { fmt.Fprintln(stderr, line) }
	}
	app, err := goWidgets.NewApp()
	if err != nil {
		return fail(err)
	}
	view, err := ui.OpenReport(app, formsbackend.OpenReport(s, rep), "oddyness report "+rep.ID, nil)
	if err != nil {
		return fail(err)
	}
	if err := app.Run(view.Window()); err != nil {
		return fail(err)
	}
	return 0
}
