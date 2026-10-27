package main

import (
	"context"
	"fmt"
	"io"

	"github.com/unxed/goWidgets"
	_ "github.com/unxed/goWidgets/backends/cocoa"
	_ "github.com/unxed/goWidgets/backends/gtk"
	_ "github.com/unxed/goWidgets/backends/headless"
	_ "github.com/unxed/goWidgets/backends/qt"
	_ "github.com/unxed/goWidgets/backends/win32"

	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/ui"
)

// runForm выполняет `oddyness form <config-dir> <db-file> <catalog>`: окно формы
// справочника; сохранённая запись попадает в базу. Backend выбирает goWidgets
// (переменная goWidgets_BACKEND задаёт его жёстко).
func runForm(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "usage: oddyness form <config-dir> <db-file> <catalog>")
		return 2
	}
	m, err := metadata.Load(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	c, err := findCatalog(m, args[2])
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	s, err := sqlite.Open(args[1], m)
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	defer s.Close()

	app, err := goWidgets.NewApp()
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	var savedID int64
	form := forms.New(c, func(values map[string]any) error {
		id, err := s.Insert(context.Background(), c.ID, values)
		if err != nil {
			return err
		}
		savedID = id
		return nil
	})
	view, err := ui.Open(app, form, "oddyness "+c.ID, nil)
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	if err := app.Run(view.Window()); err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	if savedID == 0 {
		fmt.Fprintln(stderr, "oddyness: the form was closed without saving")
		return 1
	}
	fmt.Fprintf(stdout, "id %d\n", savedID)
	return 0
}
