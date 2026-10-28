// Package forms — состояние и команды формы справочника без привязки к виджетам.
//
// Форма строится из метаданных (internal/metadata), хранит введённые тексты,
// проверяет их локально (обязательность, тип, точность чисел) и вызывает
// сохранение только когда ошибок нет. Окно (goWidgets) лишь отражает это
// состояние и пересылает команды; правила проверки живут здесь. Контролы имеют
// стабильные идентификаторы <справочник>/f/<поле> и <справочник>/b/<кнопка>.
package forms

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

var (
	// ErrInvalid — форма содержит ошибки, сохранение не выполнено.
	ErrInvalid = errors.New("forms: the form has errors")
	// ErrClosed — форма закрыта, команды не принимаются.
	ErrClosed = errors.New("forms: the form is closed")
	// ErrUnknownControl — нет контрола с таким идентификатором.
	ErrUnknownControl = errors.New("forms: unknown control")
	// ErrWrongControl — команда не подходит контролу (текст в флажок и т. п.).
	ErrWrongControl = errors.New("forms: the command does not apply to this control")
)

// Kind — вид контрола.
type Kind string

const (
	Edit     Kind = "edit"
	CheckBox Kind = "checkbox"
	Button   Kind = "button"
)

// Control — описание контрола формы.
type Control struct {
	ID    string
	Kind  Kind
	Label string
	Field string // id поля метаданных; пусто у кнопок
}

// SaveFunc сохраняет значения: ключ — id поля, значения — string, bool,
// value.Number и value.Ref (nil для пустой ссылки). Ошибка остаётся в форме.
type SaveFunc func(values map[string]any) error

// Form — состояние формы одного справочника.
type Form struct {
	id, name string
	controls []Control
	index    map[string]int
	fields   map[string]metadata.Field
	texts    map[string]string // id контрола -> текст
	checks   map[string]bool
	errs     map[string]string
	formErr  string
	focus    int
	open     bool
	save     SaveFunc
	saved    map[string]any
	keep     bool // успешное сохранение не закрывает форму (окно каталога)
}

// New создаёт открытую пустую форму справочника; фокус на первом контроле. После полей идут кнопки «Сохранить» и
// «Закрыть».
func New(c *metadata.Catalog, save SaveFunc) *Form {
	return newForm(c.ID, c.Name, c.Fields, save, true)
}

// NewFields создаёт форму без кнопок из произвольного набора полей (шапка и строка документа): команды Save и Close
// вызывает владелец формы (forms.Documents), а не кнопки. id попадает в идентификаторы контролов
// `<id>/f/<поле>`. Набор полей не должен быть пустым.
func NewFields(id, name string, fields []metadata.Field, save SaveFunc) *Form {
	return newForm(id, name, fields, save, false)
}

func newForm(id, name string, fields []metadata.Field, save SaveFunc, buttons bool) *Form {
	f := &Form{
		id: id, name: name, index: map[string]int{}, fields: map[string]metadata.Field{},
		texts: map[string]string{}, checks: map[string]bool{}, errs: map[string]string{},
		open: true, save: save,
	}
	add := func(ctl Control) {
		f.index[ctl.ID] = len(f.controls)
		f.controls = append(f.controls, ctl)
	}
	for _, fld := range fields {
		kind := Edit
		if fld.Type == metadata.TypeBoolean {
			kind = CheckBox
		}
		cid := FieldControlID(id, fld.ID)
		f.fields[cid] = fld
		add(Control{ID: cid, Kind: kind, Label: fld.Name, Field: fld.ID})
	}
	if buttons {
		add(Control{ID: ButtonControlID(id, "save"), Kind: Button, Label: "Сохранить"})
		add(Control{ID: ButtonControlID(id, "close"), Kind: Button, Label: "Закрыть"})
	}
	return f
}

// FieldControlID возвращает идентификатор контрола поля.
func FieldControlID(catalogID, fieldID string) string { return catalogID + "/f/" + fieldID }

// ButtonControlID возвращает идентификатор кнопки ("save" или "close").
func ButtonControlID(catalogID, button string) string { return catalogID + "/b/" + button }

// Controls возвращает контролы в порядке обхода фокуса.
func (f *Form) Controls() []Control { return append([]Control(nil), f.controls...) }

// IsOpen сообщает, открыта ли форма.
func (f *Form) IsOpen() bool { return f.open }

func (f *Form) control(id string, want Kind) (Control, error) {
	if !f.open {
		return Control{}, ErrClosed
	}
	i, ok := f.index[id]
	if !ok {
		return Control{}, fmt.Errorf("%w: %q", ErrUnknownControl, id)
	}
	c := f.controls[i]
	if want != "" && c.Kind != want {
		return Control{}, fmt.Errorf("%w: %q is a %s", ErrWrongControl, id, c.Kind)
	}
	return c, nil
}

// SetText вводит текст в поле ввода. Ошибка поля сбрасывается: её покажет
// следующее сохранение.
func (f *Form) SetText(id, text string) error {
	if _, err := f.control(id, Edit); err != nil {
		return err
	}
	f.texts[id] = text
	delete(f.errs, id)
	f.formErr = ""
	return nil
}

// SetChecked ставит или снимает флажок.
func (f *Form) SetChecked(id string, on bool) error {
	if _, err := f.control(id, CheckBox); err != nil {
		return err
	}
	f.checks[id] = on
	f.formErr = ""
	return nil
}

// Text возвращает введённый текст поля.
func (f *Form) Text(id string) string { return f.texts[id] }

// Checked возвращает состояние флажка.
func (f *Form) Checked(id string) bool { return f.checks[id] }

// Error возвращает сообщение об ошибке контрола ("" — нет).
func (f *Form) Error(id string) string { return f.errs[id] }

// FormError возвращает ошибку сохранения, не относящуюся к одному полю.
func (f *Form) FormError() string { return f.formErr }

// SetFormError ставит ошибку формы, не относящуюся к одному полю (ошибка команды владельца формы); "" снимает её.
func (f *Form) SetFormError(msg string) { f.formErr = msg }

// Focused возвращает идентификатор контрола в фокусе.
func (f *Form) Focused() string {
	if len(f.controls) == 0 {
		return ""
	}
	return f.controls[f.focus].ID
}

// Focus переводит фокус на контрол.
func (f *Form) Focus(id string) error {
	if _, err := f.control(id, ""); err != nil {
		return err
	}
	f.focus = f.index[id]
	return nil
}

// FocusNext и FocusPrev обходят контролы по кругу.
func (f *Form) FocusNext() error { return f.step(1) }
func (f *Form) FocusPrev() error { return f.step(-1) }

func (f *Form) step(d int) error {
	if !f.open {
		return ErrClosed
	}
	if len(f.controls) == 0 {
		return nil
	}
	f.focus = (f.focus + d + len(f.controls)) % len(f.controls)
	return nil
}

// Close закрывает форму без сохранения.
func (f *Form) Close() { f.open = false }

// Saved возвращает сохранённые значения (nil, пока сохранения не было).
func (f *Form) Saved() map[string]any { return f.saved }

// Save проверяет все поля. При ошибках они запоминаются, фокус уходит на
// первое неверное поле, сохранение не вызывается, результат — ErrInvalid.
// Без ошибок вызывается SaveFunc: её ошибка остаётся в FormError и форма
// открыта; успех закрывает форму.
func (f *Form) Save() error {
	if !f.open {
		return ErrClosed
	}
	f.errs = map[string]string{}
	f.formErr = ""
	values := map[string]any{}
	first := -1
	for _, ctl := range f.controls {
		if ctl.Kind == Button {
			continue
		}
		fld := f.fields[ctl.ID]
		v, msg := f.parse(ctl, fld)
		if msg != "" {
			f.errs[ctl.ID] = msg
			if first < 0 {
				first = f.index[ctl.ID]
			}
			continue
		}
		values[fld.ID] = v
	}
	if first >= 0 {
		f.focus = first
		return ErrInvalid
	}
	if f.save != nil {
		if err := f.save(values); err != nil {
			f.formErr = err.Error()
			return err
		}
	}
	f.saved = values
	if !f.keep {
		f.open = false
	}
	return nil
}

// KeepOpen оставляет форму открытой после успешного сохранения: так работает карточка в окне каталога,
// где после записи можно продолжить правку или выбрать другую запись.
func (f *Form) KeepOpen(keep bool) { f.keep = keep }

// Reset очищает форму для новой записи: тексты, флажки, ошибки; фокус на первом поле.
func (f *Form) Reset() error { return f.Load(nil) }

// Load заполняет форму значениями записи (string, bool, value.Number, value.Ref или nil; отсутствующее поле — пусто),
// сбрасывает ошибки и ставит фокус на первое поле. Значение неподходящего типа — ошибка, форма не меняется.
func (f *Form) Load(values map[string]any) error {
	if !f.open {
		return ErrClosed
	}
	texts, checks := map[string]string{}, map[string]bool{}
	for _, ctl := range f.controls {
		if ctl.Kind == Button {
			continue
		}
		fld := f.fields[ctl.ID]
		v, ok := values[fld.ID]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case bool:
			if ctl.Kind != CheckBox {
				return fmt.Errorf("forms: field %q: unexpected bool", fld.ID)
			}
			checks[ctl.ID] = x
		case string:
			if ctl.Kind != Edit || fld.Type == metadata.TypeNumber || fld.Type == metadata.TypeRef {
				return fmt.Errorf("forms: field %q: unexpected string", fld.ID)
			}
			texts[ctl.ID] = x
		case value.Number:
			if fld.Type != metadata.TypeNumber {
				return fmt.Errorf("forms: field %q: unexpected number", fld.ID)
			}
			texts[ctl.ID] = x.String()
		case value.Ref:
			if fld.Type != metadata.TypeRef {
				return fmt.Errorf("forms: field %q: unexpected reference", fld.ID)
			}
			texts[ctl.ID] = strconv.FormatInt(x.ID, 10)
		default:
			return fmt.Errorf("forms: field %q: unsupported value %T", fld.ID, v)
		}
	}
	f.texts, f.checks, f.errs, f.formErr, f.focus = texts, checks, map[string]string{}, "", 0
	return nil
}

// parse превращает введённый текст в значение поля; msg != "" — ошибка поля.
func (f *Form) parse(ctl Control, fld metadata.Field) (v any, msg string) {
	if ctl.Kind == CheckBox {
		return f.checks[ctl.ID], ""
	}
	text := f.texts[ctl.ID]
	if fld.Type == metadata.TypeRef {
		if strings.TrimSpace(text) == "" {
			if fld.Required {
				return nil, "required"
			}
			return nil, ""
		}
		id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil || id <= 0 {
			return nil, "reference must be a positive record number or empty"
		}
		return value.Ref{Catalog: fld.Target, ID: id}, ""
	}
	if strings.TrimSpace(text) == "" {
		return nil, "required"
	}
	if fld.Type == metadata.TypeNumber {
		n, err := value.Parse(strings.TrimSpace(text))
		if err != nil {
			return nil, "not a valid number"
		}
		if err := n.CheckPrecision(fld.Digits, fld.Scale); err != nil {
			return nil, err.Error()
		}
		return n, ""
	}
	return text, ""
}

// Snapshot — стабильный текстовый снимок формы для проверки раскладки и состояния.
func (f *Form) Snapshot() string {
	var sb strings.Builder
	state := "open"
	if !f.open {
		state = "closed"
	}
	fmt.Fprintf(&sb, "form %s %q [%s]\n", f.id, f.name, state)
	for i, c := range f.controls {
		mark := ' '
		if i == f.focus {
			mark = '*'
		}
		fmt.Fprintf(&sb, "%c %s %s %q", mark, c.ID, c.Kind, c.Label)
		switch c.Kind {
		case Edit:
			fmt.Fprintf(&sb, " = %q", f.texts[c.ID])
		case CheckBox:
			if f.checks[c.ID] {
				sb.WriteString(" = [x]")
			} else {
				sb.WriteString(" = [ ]")
			}
		}
		if e := f.errs[c.ID]; e != "" {
			fmt.Fprintf(&sb, " !error: %s", e)
		}
		sb.WriteByte('\n')
	}
	if f.formErr != "" {
		fmt.Fprintf(&sb, "! %s\n", f.formErr)
	}
	return sb.String()
}
