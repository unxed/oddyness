package ui

import (
	"github.com/unxed/goWidgets"
	"github.com/unxed/oddyness/internal/forms"
)

// CatalogView — окно каталога: список записей (goWidgets.Table), кнопка «Новый» и карточка выбранной записи.
// Состояние и правила — в forms.Catalog; окно только отражает их и пересылает команды.
type CatalogView struct {
	*View
	cat   *forms.Catalog
	table *goWidgets.Table
	newB  *goWidgets.Button
}

// OpenCatalog создаёт окно каталога. Выбор строки загружает запись в карточку, «Новый» очищает карточку,
// «Сохранить» пишет через ядро (добавление или обновление выбранной) и обновляет список; «Закрыть» и крестик
// завершают окно (onDone, по умолчанию app.Quit).
func OpenCatalog(app *goWidgets.App, cat *forms.Catalog, title string, onDone func()) (*CatalogView, error) {
	cv := &CatalogView{cat: cat}
	v, err := open(app, cat.Card(), title, onDone, 640, 760, func(v *View) error {
		cv.View = v
		cols := make([]goWidgets.TableColumn, 0, len(cat.Columns()))
		for i, c := range cat.Columns() {
			col := goWidgets.TableColumn{Title: c}
			if i == 0 {
				col.Align = goWidgets.AlignRight
			}
			cols = append(cols, col)
		}
		var err error
		if cv.table, err = v.win.AddTable(cols); err != nil {
			return err
		}
		cv.table.SetRows(cat.Rows())
		cv.table.Selected.On(app.Scope(), func(i int) { cv.pick(i) })
		if cv.newB, err = v.win.AddButton("Новый"); err != nil {
			return err
		}
		cv.newB.Clicked.On(app.Scope(), func(goWidgets.ClickInfo) { cv.pick(-1) })
		return nil
	})
	if err != nil {
		return nil, err
	}
	cv.View = v
	v.afterSave = cv.reloadList
	return cv, nil
}

// pick переносит выбор в состояние каталога и отражает загруженную запись в виджетах карточки.
func (cv *CatalogView) pick(i int) {
	if err := cv.cat.Select(i); err != nil {
		cv.formErr.Text.Set(err.Error())
		return
	}
	cv.panel.mirror()
	// Выбор строки пользователем уже лежит в таблице; программный Select лишь для «Новый» при выбранной строке.
	// Лишний Select(-1) headless-backend повторяет сообщением о выборе: оно приходило после выбора пользователя и
	// снова открывало пустую карточку (тест окна каталога иногда краснел именно так).
	if i < 0 && cv.table.SelectedIndex() != -1 {
		cv.table.Select(-1)
	}
	cv.refresh(true)
}

// reloadList показывает список из состояния каталога и выбирает сохранённую запись.
func (cv *CatalogView) reloadList() {
	cv.table.SetRows(cv.cat.Rows())
	if i, _ := cv.cat.Selected(); i >= 0 {
		cv.table.Select(i)
	}
}

// Table возвращает таблицу списка, NewButton — кнопку «Новый» (нужны тестам и прокликивающему harness).
func (cv *CatalogView) Table() *goWidgets.Table      { return cv.table }
func (cv *CatalogView) NewButton() *goWidgets.Button { return cv.newB }
