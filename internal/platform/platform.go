// Package platform — прикладное ядро: связывает метаданные, хранилище и BSL.
// Здесь живёт проведение документов: событие проведения из метаданных выполняет
// процедуру BSL из конфигурации, а статус документа и движения регистров пишутся
// одной транзакцией хранилища. Имён складских объектов платформа не знает, скрытых
// Go-обработчиков конкретных документов нет: вся прикладная логика — в BSL.
package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/unxed/oddyness/internal/bsl"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
	"github.com/unxed/oddyness/internal/vm"
)

// MovementFunc — встроенная функция BSL: Движение(Регистр, значения измерений и
// ресурсов по порядку описания в метаданных). Она только готовит движение; в
// регистр оно попадает после успешного завершения обработчика, вместе со статусом.
const MovementFunc = "Движение"

// ErrNoHandler — у вида документа нет обработчика проведения.
var ErrNoHandler = errors.New("platform: the document kind has no posting handler")

// Options ограничивают выполнение обработчика (нулевое значение — предел VM по умолчанию).
type Options struct {
	MaxSteps int64
}

type handler struct {
	file  string
	prog  *vm.Program
	entry vm.Entry
}

// Platform выполняет прикладные операции над открытым хранилищем.
type Platform struct {
	store    *sqlite.Store
	handlers map[string]*handler // id вида документа -> обработчик проведения
	opt      Options
}

var hostDecls = []vm.HostDecl{{Name: MovementFunc, MinArgs: 2, MaxArgs: -1}}

// Open загружает и компилирует обработчики проведения из каталога конфигурации.
// Ошибка файла (нет файла, разбор, компиляция, нет процедуры, не один параметр)
// называет файл и позицию: платформа не открывается с неисправным обработчиком.
func Open(store *sqlite.Store, configDir string, opt Options) (*Platform, error) {
	p := &Platform{store: store, handlers: map[string]*handler{}, opt: opt}
	for _, d := range store.Model().Documents {
		if d.Posting == nil {
			continue
		}
		h, err := loadHandler(configDir, d.Posting)
		if err != nil {
			return nil, fmt.Errorf("document %q: %w", d.ID, err)
		}
		p.handlers[d.ID] = h
	}
	return p, nil
}

func loadHandler(dir string, pst *metadata.Posting) (*handler, error) {
	path := filepath.Join(dir, filepath.FromSlash(pst.File))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("posting handler: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s: file is not valid UTF-8", path)
	}
	m, err := bsl.Parse(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))
	if err != nil {
		return nil, fmt.Errorf("%s:%v", path, err)
	}
	prog, err := vm.CompileWith(m, hostDecls)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	entry, err := prog.Entry(pst.Procedure)
	if err != nil {
		return nil, fmt.Errorf("%s: procedure %q not found", path, pst.Procedure)
	}
	if n := prog.NumParams(entry); n != 1 {
		return nil, fmt.Errorf("%s: procedure %q must take exactly one parameter (the document), it takes %d", path, pst.Procedure, n)
	}
	return &handler{file: path, prog: prog, entry: entry}, nil
}

// toValue превращает значение поля хранилища в значение BSL.
func toValue(v any) (vm.Value, error) {
	switch x := v.(type) {
	case nil:
		return vm.Value{}, nil
	case string:
		return vm.StringValue(x), nil
	case bool:
		return vm.BoolValue(x), nil
	case value.Number:
		return vm.NumberValue(x), nil
	case sqlite.Ref:
		return vm.RefValue(x.Catalog, x.ID), nil
	}
	return vm.Value{}, fmt.Errorf("unsupported field value %T", v)
}

// documentRow собирает объект документа для BSL: поля шапки и табличные части
// (таблицы строк) по именам из метаданных.
func documentRow(d *metadata.Document, doc sqlite.Document) (vm.Value, error) {
	var cols []string
	var vals []vm.Value
	for _, f := range d.Fields {
		v, err := toValue(doc.Header[f.ID])
		if err != nil {
			return vm.Value{}, fmt.Errorf("field %q: %w", f.ID, err)
		}
		cols, vals = append(cols, f.Name), append(vals, v)
	}
	for _, t := range d.Tables {
		tcols := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			tcols[i] = f.Name
		}
		var rows [][]vm.Value
		for _, rec := range doc.Tables[t.ID] {
			row := make([]vm.Value, len(t.Fields))
			for i, f := range t.Fields {
				v, err := toValue(rec.Values[f.ID])
				if err != nil {
					return vm.Value{}, fmt.Errorf("table %q field %q: %w", t.ID, f.ID, err)
				}
				row[i] = v
			}
			rows = append(rows, row)
		}
		tbl, err := vm.NewTable(tcols, rows)
		if err != nil {
			return vm.Value{}, err
		}
		cols, vals = append(cols, t.Name), append(vals, vm.TableValue(tbl))
	}
	return vm.NewRow(cols, vals)
}

// fromValue приводит значение BSL к значению поля хранилища по типу поля.
func fromValue(f metadata.Field, v vm.Value) (any, error) {
	switch f.Type {
	case metadata.TypeString, metadata.TypeDate:
		if v.Kind() != vm.String {
			return nil, fmt.Errorf("expected String, got %s", v.Kind())
		}
		return v.StringValue(), nil
	case metadata.TypeBoolean:
		if v.Kind() != vm.Bool {
			return nil, fmt.Errorf("expected Boolean, got %s", v.Kind())
		}
		return v.Bool(), nil
	case metadata.TypeNumber:
		if v.Kind() != vm.Number {
			return nil, fmt.Errorf("expected Number, got %s", v.Kind())
		}
		return v.Number(), nil
	default: // ref
		switch v.Kind() {
		case vm.Undefined:
			return nil, nil
		case vm.Reference:
			c, id := v.RefParts()
			if c != f.Target {
				return nil, fmt.Errorf("reference to catalog %q, expected %q", c, f.Target)
			}
			return sqlite.Ref{Catalog: c, ID: id}, nil
		}
		return nil, fmt.Errorf("expected a reference to %q, got %s", f.Target, v.Kind())
	}
}

// movementCollector принимает вызовы Движение и копит строки по регистрам.
type movementCollector struct {
	model *metadata.Model
	rows  map[string][]map[string]any
}

func (c *movementCollector) host(name string, args []vm.Value) (vm.Value, error) {
	if args[0].Kind() != vm.String {
		return vm.Value{}, fmt.Errorf("the register must be a String (name or id), got %s", args[0].Kind())
	}
	key := args[0].StringValue()
	var reg *metadata.Register
	for i := range c.model.Registers {
		r := &c.model.Registers[i]
		if r.ID == key || strings.EqualFold(r.Name, key) {
			reg = r
		}
	}
	if reg == nil {
		return vm.Value{}, fmt.Errorf("unknown register %q", key)
	}
	fields := append(append([]metadata.Field(nil), reg.Dimensions...), reg.Resources...)
	if len(args)-1 != len(fields) {
		return vm.Value{}, fmt.Errorf("register %q takes %d values (%d dimensions, %d resources), got %d",
			reg.Name, len(fields), len(reg.Dimensions), len(reg.Resources), len(args)-1)
	}
	row := make(map[string]any, len(fields))
	for i, f := range fields {
		v, err := fromValue(f, args[i+1])
		if err != nil {
			return vm.Value{}, fmt.Errorf("argument %d (%s): %w", i+2, f.Name, err)
		}
		row[f.ID] = v
	}
	c.rows[reg.ID] = append(c.rows[reg.ID], row)
	return vm.Value{}, nil
}

// Post проводит документ: выполняет обработчик из метаданных и в одной транзакции
// заменяет движения документа во всех регистрах и ставит признак проведения.
// Любая ошибка обработчика, проверки движений или записи откатывает всё. Повторное
// проведение заменяет движения тем же набором и остатки не удваивает.
func (p *Platform) Post(ctx context.Context, docID string, id int64) error {
	h := p.handlers[docID]
	if h == nil {
		return fmt.Errorf("%w: %q", ErrNoHandler, docID)
	}
	model := p.store.Model()
	var d *metadata.Document
	for i := range model.Documents {
		if model.Documents[i].ID == docID {
			d = &model.Documents[i]
		}
	}
	return p.store.Tx(ctx, func(tx *sqlite.Tx) error {
		doc, err := tx.GetDocument(docID, id)
		if err != nil {
			return err
		}
		row, err := documentRow(d, doc)
		if err != nil {
			return err
		}
		col := &movementCollector{model: model, rows: map[string][]map[string]any{}}
		_, err = h.prog.NewMachine().Call(ctx, h.entry, []vm.Value{row}, vm.Options{MaxSteps: p.opt.MaxSteps, Host: col.host})
		if err != nil {
			return fmt.Errorf("posting %s %d (%s): %w", docID, id, h.file, err)
		}
		for _, r := range model.Registers {
			if err := tx.ReplaceMovements(r.ID, docID, id, col.rows[r.ID]); err != nil {
				return err
			}
		}
		return tx.SetPosted(docID, id, true)
	})
}
