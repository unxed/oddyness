package forms

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

// Row — запись справочника для списка: номер и значения полей по id.
type Row struct {
	ID     int64
	Values map[string]any
}

// Backend — доступ окна каталога к ядру. Все правила хранения (уникальность, ссылки, точность) остаются за ядром;
// его ошибка показывается в форме.
type Backend interface {
	List() ([]Row, error)
	Insert(values map[string]any) (int64, error)
	Update(id int64, values map[string]any) error
}

// Catalog — состояние окна каталога: список записей и карточка выбранной (или новой) записи. Строится из метаданных,
// как и Form, и не знает о виджетах. Сохранение карточки добавляет запись или обновляет выбранную через Backend,
// затем перечитывает список и оставляет сохранённую запись выбранной.
type Catalog struct {
	catalog  *metadata.Catalog
	be       Backend
	card     *Form
	rows     []Row
	selected int // индекс выбранной строки, -1 — новая запись
	listErr  string
}

// NewCatalog читает список и открывает пустую карточку новой записи.
func NewCatalog(c *metadata.Catalog, be Backend) (*Catalog, error) {
	cat := &Catalog{catalog: c, be: be, selected: -1}
	cat.card = New(c, cat.save)
	cat.card.KeepOpen(true)
	if err := cat.Refresh(); err != nil {
		return nil, err
	}
	return cat, nil
}

// Card возвращает форму карточки (команды ввода и сохранения идут через неё; Save карточки сохраняет в ядро).
func (c *Catalog) Card() *Form { return c.card }

// Columns — заголовки списка: номер записи и имена полей.
func (c *Catalog) Columns() []string {
	cols := []string{"№"}
	for _, f := range c.catalog.Fields {
		cols = append(cols, f.Name)
	}
	return cols
}

// Rows — строки списка в тексте (числа — точной записью, ссылка — номером, булево — «да»/«нет»).
func (c *Catalog) Rows() [][]string {
	out := make([][]string, len(c.rows))
	for i, r := range c.rows {
		row := []string{strconv.FormatInt(r.ID, 10)}
		for _, f := range c.catalog.Fields {
			row = append(row, cell(r.Values[f.ID]))
		}
		out[i] = row
	}
	return out
}

func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "да"
		}
		return "нет"
	case value.Number:
		return x.String()
	case value.Ref:
		return strconv.FormatInt(x.ID, 10)
	}
	return fmt.Sprint(v)
}

// Selected — индекс выбранной строки (-1 — карточка новой записи) и её номер (0 — новая).
func (c *Catalog) Selected() (index int, id int64) {
	if c.selected < 0 {
		return -1, 0
	}
	return c.selected, c.rows[c.selected].ID
}

// ListError — ошибка чтения списка (пусто, если её нет).
func (c *Catalog) ListError() string { return c.listErr }

// Refresh перечитывает список; выбор сохраняется по номеру записи, пропавшая запись — новая карточка.
func (c *Catalog) Refresh() error {
	_, id := c.Selected()
	rows, err := c.be.List()
	if err != nil {
		c.listErr = err.Error()
		return err
	}
	c.listErr = ""
	c.rows, c.selected = rows, -1
	for i, r := range rows {
		if r.ID == id && id != 0 {
			c.selected = i
		}
	}
	return nil
}

// Select выбирает строку списка и загружает её в карточку; -1 — новая запись.
func (c *Catalog) Select(i int) error {
	if i < -1 || i >= len(c.rows) {
		return fmt.Errorf("forms: row %d is out of range 0..%d", i, len(c.rows)-1)
	}
	if i == -1 {
		return c.New()
	}
	if err := c.card.Load(c.rows[i].Values); err != nil {
		return err
	}
	c.selected = i
	return nil
}

// New открывает пустую карточку новой записи.
func (c *Catalog) New() error {
	if err := c.card.Reset(); err != nil {
		return err
	}
	c.selected = -1
	return nil
}

// Close закрывает окно каталога (карточку); несохранённый ввод пропадает.
func (c *Catalog) Close() { c.card.Close() }

// IsOpen сообщает, открыт ли каталог.
func (c *Catalog) IsOpen() bool { return c.card.IsOpen() }

func (c *Catalog) save(values map[string]any) error {
	_, id := c.Selected()
	if id == 0 {
		nid, err := c.be.Insert(values)
		if err != nil {
			return err
		}
		id = nid
	} else if err := c.be.Update(id, values); err != nil {
		return err
	}
	c.selected = -1
	rows, err := c.be.List()
	if err != nil {
		return fmt.Errorf("saved, but the list could not be read: %w", err)
	}
	c.rows = rows
	for i, r := range rows {
		if r.ID == id {
			c.selected = i
		}
	}
	return nil
}

// Snapshot — стабильный текстовый снимок: заголовок, список (маркер выбранной строки) и карточка.
func (c *Catalog) Snapshot() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "catalog %s %q [%s]\n", c.catalog.ID, c.catalog.Name, map[bool]string{true: "open", false: "closed"}[c.IsOpen()])
	fmt.Fprintf(&sb, "columns: %s\n", strings.Join(c.Columns(), " | "))
	for i, r := range c.Rows() {
		mark := ' '
		if i == c.selected {
			mark = '>'
		}
		fmt.Fprintf(&sb, "%c %s\n", mark, strings.Join(r, " | "))
	}
	if c.listErr != "" {
		fmt.Fprintf(&sb, "! %s\n", c.listErr)
	}
	sb.WriteString(c.card.Snapshot())
	return sb.String()
}
