// Package ui привязывает форму справочника (internal/forms) к окну goWidgets.
// Окно только отражает состояние формы и пересылает ей команды: правила
// проверки и сохранения остаются в forms. Общих контролов здесь нет — только
// те, что даёт goWidgets (AGENTS.md п. 7).
package ui

import (
	"github.com/unxed/goWidgets"
	"github.com/unxed/oddyness/internal/forms"
)

// View — окно формы справочника.
type View struct {
	*panel
	app     *goWidgets.App
	win     *goWidgets.Window
	onDone  func()
	saveID  string // кнопки: первая — «Сохранить», вторая — «Закрыть»
	closeID string

	afterSave func() // вызывается после успешного «Сохранить» (окно каталога перечитывает список)
}

// Open создаёт окно для формы: подпись, поле или флажок и строка ошибки на
// каждое поле, затем кнопки и строка ошибки формы. onDone вызывается, когда
// форма закрыта (успешным сохранением, кнопкой или крестиком); nil означает
// app.Quit.
func Open(app *goWidgets.App, form *forms.Form, title string, onDone func()) (*View, error) {
	return open(app, form, title, onDone, 420, 560, nil)
}

// open строит окно; before (если задан) добавляет виджеты над карточкой.
func open(app *goWidgets.App, form *forms.Form, title string, onDone func(), w, h float64, before func(*View) error) (*View, error) {
	win, err := app.NewWindow(title, w, h)
	if err != nil {
		return nil, err
	}
	if onDone == nil {
		onDone = app.Quit
	}
	v := &View{app: app, win: win, onDone: onDone}
	for _, c := range form.Controls() {
		if c.Kind == forms.Button {
			if v.saveID == "" {
				v.saveID = c.ID
			} else {
				v.closeID = c.ID
			}
		}
	}
	if before != nil {
		if err := before(v); err != nil {
			return nil, err
		}
	}
	if v.panel, err = newPanel(app, win, form, v.click, func() { v.refresh(false) }); err != nil {
		return nil, err
	}
	win.Closing.On(app.Scope(), func(*goWidgets.CloseRequest) {
		form.Close()
		v.refresh(false)
	})
	v.refresh(true)
	return v, nil
}

func (v *View) click(id string) {
	switch id {
	case v.saveID:
		if err := v.form.Save(); err == nil && v.afterSave != nil {
			v.afterSave()
		} // иначе результат виден через refresh: ошибки полей, ошибка формы, закрытие
	case v.closeID:
		v.form.Close()
	}
	v.refresh(true)
}

// refresh отражает состояние формы: ошибки полей и формы; закрытие формы
// завершает работу окна. Фокус перемещается только по команде формы (moveFocus):
// форма не знает, куда пользователь перешёл Tab-ом, и отражение ввода не должно
// возвращать фокус на прежний контрол (нативный сценарий нашёл именно эту ошибку).
func (v *View) refresh(moveFocus bool) {
	v.labels()
	if v.form.IsOpen() {
		if moveFocus {
			v.focus()
		}
		return
	}
	v.onDone()
}

// Window возвращает окно goWidgets.
func (v *View) Window() *goWidgets.Window { return v.win }
