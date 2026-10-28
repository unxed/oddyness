package main

import (
	"context"
	"fmt"
	"io"

	"github.com/unxed/goWidgets"

	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/ui"
)

// storeBackend связывает окно каталога с ядром: все проверки и ошибки — ядра.
type storeBackend struct {
	s *sqlite.Store
	c *metadata.Catalog
}

// CanWrite сообщает, разрешена ли записи роль сессии (forms.WriteGate).
func (b storeBackend) CanWrite() bool { return b.s.CanWrite() }

func (b storeBackend) List() ([]forms.Row, error) {
	recs, err := b.s.List(context.Background(), b.c.ID)
	if err != nil {
		return nil, err
	}
	rows := make([]forms.Row, len(recs))
	for i, r := range recs {
		rows[i] = forms.Row{ID: r.ID, Values: r.Values}
	}
	return rows, nil
}

func (b storeBackend) Insert(values map[string]any) (int64, error) {
	return b.s.Insert(context.Background(), b.c.ID, values)
}

func (b storeBackend) Update(id int64, values map[string]any) error {
	return b.s.Update(context.Background(), b.c.ID, id, values)
}

// runApp выполняет `oddyness app <config-dir> <db-file> <catalog>`: окно каталога со списком записей и карточкой;
// новая запись добавляется, выбранная обновляется через ядро. Окно закрывается кнопкой «Закрыть» или крестиком.
func runApp(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "usage: oddyness app <config-dir> <db-file> <catalog>")
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
	c, err := findCatalog(m, args[2])
	if err != nil {
		return fail(err)
	}
	s, err := openSession(args[1], m)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	cat, err := forms.NewCatalog(c, storeBackend{s, c})
	if err != nil {
		return fail(err)
	}
	app, err := goWidgets.NewApp()
	if err != nil {
		return fail(err)
	}
	view, err := ui.OpenCatalog(app, cat, "oddyness app "+c.ID, nil)
	if err != nil {
		return fail(err)
	}
	if err := app.Run(view.Window()); err != nil {
		return fail(err)
	}
	return 0
}
