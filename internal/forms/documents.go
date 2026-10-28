package forms

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
)

var (
	// ErrNotSaved — команда требует сохранённого документа (отмена проведения).
	ErrNotSaved = errors.New("forms: the document is not saved yet")
	// ErrNoTable — у вида документа нет табличной части, команда строк неприменима.
	ErrNoTable = errors.New("forms: the document kind has no table part")
	// ErrNoLine — команда требует выбранной строки.
	ErrNoLine = errors.New("forms: no line is selected")
)

// DocRecord — документ для списка: номер, признак проведения, шапка и строки единственной табличной части
// (значения типизированы так же, как в Form: string, value.Number, value.Ref).
type DocRecord struct {
	ID     int64
	Posted bool
	Header map[string]any
	Lines  []map[string]any
}

// DocBackend — доступ окна документов к ядру. Правила хранения и проведения (уникальность номера, положительное
// количество, запрет правки проведённого, остаток, отказ обработчика) остаются за ядром; его ошибка показывается
// в форме документа.
type DocBackend interface {
	List() ([]DocRecord, error)
	Insert(header map[string]any, lines []map[string]any) (int64, error)
	Update(id int64, header map[string]any, lines []map[string]any) error
	Post(id int64) error
	Unpost(id int64) error
}

// Documents — состояние окна документов одного вида: список документов, шапка и строки выбранного (или нового)
// документа, карточка текущей строки. Строится из метаданных и не знает о виджетах. Строки хранятся в памяти до
// «Сохранить»: документ записывается целиком (шапка и все строки одной операцией ядра). Поддерживается вид
// документа не более чем с одной табличной частью; больше — явная ошибка при создании.
type Documents struct {
	doc      *metadata.Document
	part     *metadata.TablePart
	be       DocBackend
	header   *Form
	line     *Form // nil, если у вида нет табличной части
	rows     []DocRecord
	selected int // индекс выбранного документа, -1 — новый
	lines    []map[string]any
	lineSel  int // индекс выбранной строки, -1 — новая
	listErr  string
	readOnly bool // ядро не разрешает запись роли сессии (окно показывает команды недоступными)
}

// WriteGate — необязательная возможность backend: сообщает, разрешена ли записи роль сессии. Окно показывает недоступные
// команды; защитой служат проверки ядра, а не окно (команда, выданная мимо окна, получает отказ ядра).
type WriteGate interface{ CanWrite() bool }

// NewDocuments читает список и открывает пустой новый документ.
func NewDocuments(d *metadata.Document, be DocBackend) (*Documents, error) {
	if len(d.Tables) > 1 {
		return nil, fmt.Errorf("forms: document %q has %d table parts; the document form supports at most one", d.ID, len(d.Tables))
	}
	docs := &Documents{doc: d, be: be, selected: -1, lineSel: -1}
	if g, ok := be.(WriteGate); ok {
		docs.readOnly = !g.CanWrite()
	}
	docs.header = NewFields(d.ID, d.Name, d.Fields, docs.persist)
	docs.header.KeepOpen(true)
	if len(d.Tables) == 1 {
		docs.part = &d.Tables[0]
		docs.line = NewFields(docs.part.ID, docs.part.Name, docs.part.Fields, nil)
		docs.line.KeepOpen(true)
	}
	if err := docs.Refresh(); err != nil {
		return nil, err
	}
	return docs, nil
}

// Header возвращает форму шапки (команды ввода идут через неё; сохранение документа — Documents.Save).
func (d *Documents) Header() *Form { return d.header }

// Line возвращает форму текущей строки (nil, если у вида нет табличной части); запись строки — SaveLine.
func (d *Documents) Line() *Form { return d.line }

// ReadOnly сообщает, что роль сессии не разрешает запись: окно показывает команды записи недоступными, а чтение
// (список, выбор документа) работает. Команда записи мимо окна получает отказ ядра (ErrDenied) в Error.
func (d *Documents) ReadOnly() bool { return d.readOnly }

// HasLines сообщает, есть ли у вида документа табличная часть.
func (d *Documents) HasLines() bool { return d.part != nil }

// Columns — заголовки списка: номер, поля шапки и признак проведения.
func (d *Documents) Columns() []string {
	cols := []string{"№"}
	for _, f := range d.doc.Fields {
		cols = append(cols, f.Name)
	}
	return append(cols, "Проведён")
}

// Rows — строки списка в тексте.
func (d *Documents) Rows() [][]string {
	out := make([][]string, len(d.rows))
	for i, r := range d.rows {
		row := []string{strconv.FormatInt(r.ID, 10)}
		for _, f := range d.doc.Fields {
			row = append(row, cell(r.Header[f.ID]))
		}
		out[i] = append(row, cell(r.Posted))
	}
	return out
}

// LineColumns — заголовки строк: номер строки и поля табличной части (nil без табличной части).
func (d *Documents) LineColumns() []string {
	if d.part == nil {
		return nil
	}
	cols := []string{"№"}
	for _, f := range d.part.Fields {
		cols = append(cols, f.Name)
	}
	return cols
}

// Lines — строки выбранного документа в тексте (с несохранёнными правками).
func (d *Documents) Lines() [][]string {
	out := make([][]string, len(d.lines))
	for i, l := range d.lines {
		row := []string{strconv.Itoa(i + 1)}
		for _, f := range d.part.Fields {
			row = append(row, cell(l[f.ID]))
		}
		out[i] = row
	}
	return out
}

// Selected — индекс выбранного документа в списке (-1 — новый) и его номер (0 — новый).
func (d *Documents) Selected() (index int, id int64) {
	if d.selected < 0 {
		return -1, 0
	}
	return d.selected, d.rows[d.selected].ID
}

// SelectedLine — индекс выбранной строки (-1 — карточка новой строки).
func (d *Documents) SelectedLine() int { return d.lineSel }

// Posted сообщает, проведён ли выбранный документ (по последнему чтению из ядра).
func (d *Documents) Posted() bool { return d.selected >= 0 && d.rows[d.selected].Posted }

// ListError — ошибка чтения списка (пусто, если её нет).
func (d *Documents) ListError() string { return d.listErr }

// Error — ошибка последней команды документа (ядра или проверки); пусто, если её нет.
func (d *Documents) Error() string { return d.header.FormError() }

// IsOpen сообщает, открыто ли окно документов.
func (d *Documents) IsOpen() bool { return d.header.IsOpen() }

// Close закрывает окно документов; несохранённый ввод пропадает.
func (d *Documents) Close() {
	d.header.Close()
	if d.line != nil {
		d.line.Close()
	}
}

// Refresh перечитывает список; выбор сохраняется по номеру документа, пропавший документ — новый документ.
func (d *Documents) Refresh() error {
	_, id := d.Selected()
	rows, err := d.be.List()
	if err != nil {
		d.listErr = err.Error()
		return err
	}
	d.listErr = ""
	d.rows, d.selected = rows, -1
	for i, r := range rows {
		if r.ID == id && id != 0 {
			d.selected = i
		}
	}
	return nil
}

// Select выбирает документ списка и загружает его шапку и строки; -1 — новый документ.
func (d *Documents) Select(i int) error {
	if i < -1 || i >= len(d.rows) {
		return fmt.Errorf("forms: document row %d is out of range 0..%d", i, len(d.rows)-1)
	}
	if i == -1 {
		return d.New()
	}
	return d.load(i)
}

func (d *Documents) load(i int) error {
	if err := d.header.Load(d.rows[i].Header); err != nil {
		return err
	}
	d.selected = i
	d.lines = cloneLines(d.rows[i].Lines)
	return d.resetLine()
}

// New открывает пустой новый документ.
func (d *Documents) New() error {
	if err := d.header.Reset(); err != nil {
		return err
	}
	d.selected, d.lines = -1, nil
	return d.resetLine()
}

func (d *Documents) resetLine() error {
	d.lineSel = -1
	if d.line == nil {
		return nil
	}
	return d.line.Reset()
}

// SelectLine выбирает строку и загружает её в карточку строки; -1 — новая строка.
func (d *Documents) SelectLine(i int) error {
	if d.part == nil {
		return ErrNoTable
	}
	if i < -1 || i >= len(d.lines) {
		return fmt.Errorf("forms: line %d is out of range 0..%d", i, len(d.lines)-1)
	}
	if i == -1 {
		return d.NewLine()
	}
	if err := d.line.Load(d.lines[i]); err != nil {
		return err
	}
	d.lineSel = i
	return nil
}

// NewLine открывает пустую карточку новой строки.
func (d *Documents) NewLine() error {
	if d.part == nil {
		return ErrNoTable
	}
	return d.resetLine()
}

// SaveLine проверяет карточку строки и добавляет строку в документ (или заменяет выбранную); карточка очищается
// для следующей строки. Неверная строка остаётся в карточке с ошибками полей (ErrInvalid), документ не меняется.
// В ядро строки попадают при Save.
func (d *Documents) SaveLine() error {
	if d.part == nil {
		return ErrNoTable
	}
	var values map[string]any
	d.line.save = func(v map[string]any) error { values = v; return nil }
	if err := d.line.Save(); err != nil {
		return err
	}
	if d.lineSel >= 0 {
		d.lines[d.lineSel] = values
	} else {
		d.lines = append(d.lines, values)
	}
	return d.resetLine()
}

// DeleteLine удаляет выбранную строку из документа.
func (d *Documents) DeleteLine() error {
	if d.part == nil {
		return ErrNoTable
	}
	if d.lineSel < 0 {
		return ErrNoLine
	}
	d.lines = append(d.lines[:d.lineSel:d.lineSel], d.lines[d.lineSel+1:]...)
	return d.resetLine()
}

// Save проверяет шапку и записывает документ целиком через ядро: новый добавляется, выбранный обновляется.
// Ошибки полей шапки — ErrInvalid; ошибка ядра (дубликат номера, ErrPosted у проведённого документа, неверная
// строка) остаётся в Error, ввод сохранён. После успеха документ остаётся выбранным.
func (d *Documents) Save() error { return d.header.Save() }

func (d *Documents) persist(header map[string]any) error {
	_, id := d.Selected()
	if id == 0 {
		nid, err := d.be.Insert(header, cloneLines(d.lines))
		if err != nil {
			return err
		}
		id = nid
	} else if err := d.be.Update(id, header, cloneLines(d.lines)); err != nil {
		return err
	}
	d.selected = -1
	rows, err := d.be.List()
	if err != nil {
		return fmt.Errorf("saved, but the list could not be read: %w", err)
	}
	d.rows = rows
	for i, r := range rows {
		if r.ID == id {
			d.selected = i
			d.lines = cloneLines(r.Lines)
		}
	}
	return nil
}

// Post записывает документ и проводит его (как «Провести» в 1С); проведённый документ проводится повторно без
// записи — его правка запрещена ядром. Отказ обработчика или ядра (недостаточный остаток) оставляет документ
// записанным, но не проведённым (его причина — в Error); после команды документ перечитывается из ядра.
func (d *Documents) Post() error {
	if !d.IsOpen() {
		return ErrClosed
	}
	if !d.Posted() {
		if err := d.Save(); err != nil {
			return err
		}
	}
	_, id := d.Selected()
	if id == 0 {
		return ErrNotSaved
	}
	return d.command(id, d.be.Post)
}

// Unpost отменяет проведение выбранного документа; отказ ядра (отмена дала бы отрицательный остаток) показывается
// в Error, документ остаётся проведённым.
func (d *Documents) Unpost() error {
	if !d.IsOpen() {
		return ErrClosed
	}
	_, id := d.Selected()
	if id == 0 {
		d.header.SetFormError(ErrNotSaved.Error())
		return ErrNotSaved
	}
	return d.command(id, d.be.Unpost)
}

// command выполняет команду ядра над сохранённым документом и перечитывает его: статус и строки — из ядра.
func (d *Documents) command(id int64, run func(int64) error) error {
	err := run(id)
	if rerr := d.Refresh(); rerr != nil {
		d.header.SetFormError(rerr.Error())
		return rerr
	}
	if i, _ := d.Selected(); i >= 0 {
		if lerr := d.load(i); lerr != nil {
			return lerr
		}
	}
	if err != nil {
		d.header.SetFormError(err.Error())
	}
	return err
}

func cloneLines(lines []map[string]any) []map[string]any {
	out := make([]map[string]any, len(lines))
	for i, l := range lines {
		c := make(map[string]any, len(l))
		for k, v := range l {
			c[k] = v
		}
		out[i] = c
	}
	return out
}

// Snapshot — стабильный текстовый снимок: список документов, шапка, строки и карточка строки.
func (d *Documents) Snapshot() string {
	var sb strings.Builder
	state := "open"
	if !d.IsOpen() {
		state = "closed"
	}
	fmt.Fprintf(&sb, "documents %s %q [%s]\n", d.doc.ID, d.doc.Name, state)
	fmt.Fprintf(&sb, "columns: %s\n", strings.Join(d.Columns(), " | "))
	for i, r := range d.Rows() {
		mark := ' '
		if i == d.selected {
			mark = '>'
		}
		fmt.Fprintf(&sb, "%c %s\n", mark, strings.Join(r, " | "))
	}
	if d.listErr != "" {
		fmt.Fprintf(&sb, "! %s\n", d.listErr)
	}
	sb.WriteString(d.header.Snapshot())
	if d.part != nil {
		fmt.Fprintf(&sb, "lines: %s\n", strings.Join(d.LineColumns(), " | "))
		for i, r := range d.Lines() {
			mark := ' '
			if i == d.lineSel {
				mark = '>'
			}
			fmt.Fprintf(&sb, "%c %s\n", mark, strings.Join(r, " | "))
		}
		sb.WriteString(d.line.Snapshot())
	}
	return sb.String()
}
