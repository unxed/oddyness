package main

import (
	"fmt"
	"io"
	"os"

	"github.com/unxed/goWidgets"

	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/formsbackend"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/ui"
)

// runDocuments выполняет `oddyness documents <config-dir> <db-file> <document>`: окно документов вида со списком,
// шапкой, строками и командами сохранить, провести, отменить проведение. Окно закрывается кнопкой «Закрыть» или
// крестиком.
func runDocuments(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "usage: oddyness documents <config-dir> <db-file> <document>")
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
	d, err := findDocument(m, args[2])
	if err != nil {
		return fail(err)
	}
	s, err := openSession(args[1], m)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	pl, err := platform.Open(s, args[0], platform.Options{})
	if err != nil {
		return fail(err)
	}
	be, err := formsbackend.NewDocuments(s, pl, d.ID)
	if err != nil {
		return fail(err)
	}
	docs, err := forms.NewDocuments(d, be)
	if err != nil {
		return fail(err)
	}
	if os.Getenv("ODDYNESS_UI_TRACE") == "1" {
		ui.Trace = func(line string) { fmt.Fprintln(stderr, line) }
	}
	app, err := goWidgets.NewApp()
	if err != nil {
		return fail(err)
	}
	view, err := ui.OpenDocuments(app, docs, "oddyness documents "+d.ID, nil)
	if err != nil {
		return fail(err)
	}
	if err := app.Run(view.Window()); err != nil {
		return fail(err)
	}
	return 0
}
