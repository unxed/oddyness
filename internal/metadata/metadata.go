// Package metadata загружает версионированную текстовую модель конфигурации:
// каталог с metadata.json (формат oddyness, не XML/EDT/cf 1С). Загрузка строгая:
// неизвестные поля, версия, тип, повтор идентификатора и битая ссылка — ошибки
// с путём (и строкой, когда её можно указать точно). Модель создаётся целиком
// или не создаётся совсем; Store не меняет действующую модель при ошибке.
package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// Version — единственная поддерживаемая версия формата.
const Version = 1

// FileName — файл метаданных внутри каталога конфигурации.
const FileName = "metadata.json"

// FieldType — тип поля справочника.
type FieldType string

const (
	TypeString  FieldType = "string"
	TypeBoolean FieldType = "boolean"
	TypeNumber  FieldType = "number" // требует digits (1..32) и scale (0..digits)
	TypeRef     FieldType = "ref"    // требует target — id справочника
	TypeDate    FieldType = "date"   // дата ГГГГ-ММ-ДД
)

// Field — поле справочника.
type Field struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Type   FieldType `json:"type"`
	Digits int       `json:"digits,omitempty"`
	Scale  int       `json:"scale,omitempty"`
	Target string    `json:"target,omitempty"`
	Unique bool      `json:"unique,omitempty"` // только string: значение не повторяется в справочнике или виде документа
	// Positive требует значение больше нуля (только number): количество.
	Positive bool `json:"positive,omitempty"`
	// Required делает ссылку обязательной (только ref); остальные типы обязательны всегда.
	Required bool `json:"required,omitempty"`
}

// Catalog — справочник.
type Catalog struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

// TablePart — табличная часть документа: строки с одинаковым набором полей.
type TablePart struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

// Document — вид документа: поля шапки и табличные части.
type Document struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Fields []Field     `json:"fields"`
	Tables []TablePart `json:"tables,omitempty"`
}

// Register — регистр накопления: измерения (по чему ведётся учёт) и ресурсы
// (суммируемые числа). Каждое движение несёт регистратора (вид и номер
// документа) и номер строки; они неявные и в метаданных не описываются.
type Register struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Dimensions []Field `json:"dimensions"`
	Resources  []Field `json:"resources"`
}

// Model — загруженная модель.
type Model struct {
	Version   int        `json:"version"`
	Catalogs  []Catalog  `json:"catalogs"`
	Documents []Document `json:"documents,omitempty"`
	Registers []Register `json:"registers,omitempty"`
}

// Error — диагностика загрузки: файл, позиция (если известна), путь внутри
// документа и причина.
type Error struct {
	File      string
	Line, Col int
	Path      string
	Msg       string
}

func (e *Error) Error() string {
	var sb strings.Builder
	sb.WriteString(e.File)
	if e.Line > 0 {
		fmt.Fprintf(&sb, ":%d:%d", e.Line, e.Col)
	}
	sb.WriteString(": ")
	if e.Path != "" {
		sb.WriteString(e.Path)
		sb.WriteString(": ")
	}
	sb.WriteString(e.Msg)
	return sb.String()
}

// Load читает и проверяет конфигурацию из каталога dir.
func Load(dir string) (*Model, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	return Parse(path, data)
}

// Parse разбирает и проверяет содержимое файла; name попадает в сообщения.
func Parse(name string, data []byte) (*Model, error) {
	fail := func(offset int, path, format string, args ...any) error {
		e := &Error{File: name, Path: path, Msg: fmt.Sprintf(format, args...)}
		if offset >= 0 {
			e.Line, e.Col = position(data, offset)
		}
		return e
	}
	if !utf8.Valid(data) {
		return nil, fail(-1, "", "file is not valid UTF-8")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fail(-1, "", "file is empty")
	}
	if err := checkVersion(data, fail); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Model
	if err := dec.Decode(&m); err != nil {
		return nil, decodeError(data, err, fail)
	}
	if err := m.validate(fail); err != nil {
		return nil, err
	}
	return &m, nil
}

type failFunc func(offset int, path, format string, args ...any) error

// checkVersion проверяет версию отдельным нестрогим проходом, чтобы файл новой
// версии получил «неподдерживаемая версия», а не «неизвестное поле».
func checkVersion(data []byte, fail failFunc) error {
	var probe struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return decodeError(data, err, fail)
	}
	switch {
	case probe.Version == nil:
		return fail(-1, "version", "required field is missing")
	case *probe.Version != Version:
		return fail(offsetOf(data, `"version"`), "version", "unsupported version %d (supported: %d)", *probe.Version, Version)
	}
	return nil
}

func decodeError(data []byte, err error, fail failFunc) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		offset := int(syn.Offset)
		if offset > 0 && strings.HasPrefix(syn.Error(), "invalid character") {
			offset-- // Offset указывает за недопустимым символом
		}
		return fail(offset, "", "invalid JSON: %v", syn)
	case errors.As(err, &typ):
		return fail(int(typ.Offset), typ.Field, "wrong type: expected %s, found %s", typ.Type, typ.Value)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fail(len(data), "", "invalid JSON: unexpected end of file")
	}
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "json: unknown field "); ok {
		return fail(offsetOf(data, rest), "", "unknown field %s", rest)
	}
	return fail(-1, "", "%v", err)
}

// offsetOf возвращает смещение единственного вхождения s в data, иначе -1:
// неоднозначную позицию не угадываем.
func offsetOf(data []byte, s string) int {
	if bytes.Count(data, []byte(s)) != 1 {
		return -1
	}
	return bytes.Index(data, []byte(s))
}

// position переводит смещение в строку и колонку (в символах, с единицы).
func position(data []byte, offset int) (line, col int) {
	if offset > len(data) {
		offset = len(data)
	}
	line, col = 1, 1
	for _, r := range string(data[:offset]) {
		if r == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i, r := range id {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if i == 0 && !letter {
			return false
		}
		if !letter && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func (m *Model) validate(fail failFunc) error {
	if len(m.Catalogs) == 0 {
		return fail(-1, "catalogs", "at least one catalog is required")
	}
	ids := map[string]string{}    // id -> путь первого использования
	catalogs := map[string]bool{} // id справочников
	names := map[string]string{}
	checkID := func(path, id string) error {
		if !validID(id) {
			return fail(-1, path+".id", "invalid id %q (letters, digits, '_' and '-', starts with a letter, up to 64 characters)", id)
		}
		if first, dup := ids[id]; dup {
			return fail(-1, path+".id", "duplicate id %q (first used at %s)", id, first)
		}
		ids[id] = path
		return nil
	}
	for i, c := range m.Catalogs {
		cp := fmt.Sprintf("catalogs[%d]", i)
		if err := checkID(cp, c.ID); err != nil {
			return err
		}
		catalogs[c.ID] = true
		if c.Name == "" {
			return fail(-1, cp+".name", "name is required")
		}
		if first, dup := names[strings.ToLower(c.Name)]; dup {
			return fail(-1, cp+".name", "duplicate catalog name %q (first used at %s)", c.Name, first)
		}
		names[strings.ToLower(c.Name)] = cp
		if len(c.Fields) == 0 {
			return fail(-1, cp+".fields", "catalog %q has no fields", c.Name)
		}
	}
	checkFields := func(path, list, owner string, fields []Field) error {
		fieldNames := map[string]string{}
		for j, f := range fields {
			fp := fmt.Sprintf("%s.%s[%d]", path, list, j)
			if err := checkID(fp, f.ID); err != nil {
				return err
			}
			if f.Name == "" {
				return fail(-1, fp+".name", "name is required")
			}
			if first, dup := fieldNames[strings.ToLower(f.Name)]; dup {
				return fail(-1, fp+".name", "duplicate field name %q in %s (first used at %s)", f.Name, owner, first)
			}
			fieldNames[strings.ToLower(f.Name)] = fp
			if err := m.validateType(fp, f, catalogs, fail); err != nil {
				return err
			}
		}
		return nil
	}
	for i, c := range m.Catalogs {
		if err := checkFields(fmt.Sprintf("catalogs[%d]", i), "fields", fmt.Sprintf("catalog %q", c.Name), c.Fields); err != nil {
			return err
		}
	}
	docNames := map[string]string{}
	for i, d := range m.Documents {
		dp := fmt.Sprintf("documents[%d]", i)
		if err := checkID(dp, d.ID); err != nil {
			return err
		}
		if d.Name == "" {
			return fail(-1, dp+".name", "name is required")
		}
		if first, dup := docNames[strings.ToLower(d.Name)]; dup {
			return fail(-1, dp+".name", "duplicate document name %q (first used at %s)", d.Name, first)
		}
		docNames[strings.ToLower(d.Name)] = dp
		if len(d.Fields) == 0 {
			return fail(-1, dp+".fields", "document %q has no header fields", d.Name)
		}
		if err := checkFields(dp, "fields", fmt.Sprintf("document %q", d.Name), d.Fields); err != nil {
			return err
		}
		tableNames := map[string]string{}
		for k, tp := range d.Tables {
			tpath := fmt.Sprintf("%s.tables[%d]", dp, k)
			if err := checkID(tpath, tp.ID); err != nil {
				return err
			}
			if tp.Name == "" {
				return fail(-1, tpath+".name", "name is required")
			}
			if first, dup := tableNames[strings.ToLower(tp.Name)]; dup {
				return fail(-1, tpath+".name", "duplicate table name %q in document %q (first used at %s)", tp.Name, d.Name, first)
			}
			tableNames[strings.ToLower(tp.Name)] = tpath
			if len(tp.Fields) == 0 {
				return fail(-1, tpath+".fields", "table %q has no fields", tp.Name)
			}
			if err := checkFields(tpath, "fields", fmt.Sprintf("table %q", tp.Name), tp.Fields); err != nil {
				return err
			}
		}
	}
	regNames := map[string]string{}
	for i, r := range m.Registers {
		rp := fmt.Sprintf("registers[%d]", i)
		if err := checkID(rp, r.ID); err != nil {
			return err
		}
		if r.Name == "" {
			return fail(-1, rp+".name", "name is required")
		}
		if first, dup := regNames[strings.ToLower(r.Name)]; dup {
			return fail(-1, rp+".name", "duplicate register name %q (first used at %s)", r.Name, first)
		}
		regNames[strings.ToLower(r.Name)] = rp
		if len(r.Dimensions) == 0 {
			return fail(-1, rp+".dimensions", "register %q has no dimensions", r.Name)
		}
		if len(r.Resources) == 0 {
			return fail(-1, rp+".resources", "register %q has no resources", r.Name)
		}
		owner := fmt.Sprintf("register %q", r.Name)
		if err := checkFields(rp, "dimensions", owner, r.Dimensions); err != nil {
			return err
		}
		if err := checkFields(rp, "resources", owner, r.Resources); err != nil {
			return err
		}
		for j, f := range r.Dimensions {
			if f.Type == TypeNumber {
				return fail(-1, fmt.Sprintf("%s.dimensions[%d].type", rp, j), "a number cannot be a dimension (numbers are summed, not grouped); use a resource")
			}
		}
		dimNames := map[string]bool{}
		for _, f := range r.Dimensions {
			dimNames[strings.ToLower(f.Name)] = true
		}
		for j, f := range r.Resources {
			if f.Type != TypeNumber {
				return fail(-1, fmt.Sprintf("%s.resources[%d].type", rp, j), "a resource must be a number, got %q", f.Type)
			}
			if dimNames[strings.ToLower(f.Name)] {
				return fail(-1, fmt.Sprintf("%s.resources[%d].name", rp, j), "resource name %q repeats a dimension name", f.Name)
			}
		}
	}
	return nil
}

func (m *Model) validateType(fp string, f Field, catalogs map[string]bool, fail failFunc) error {
	switch f.Type {
	case TypeString, TypeBoolean, TypeNumber, TypeRef, TypeDate:
	default:
		return fail(-1, fp+".type", "unknown type %q (supported: string, boolean, number, ref, date)", f.Type)
	}
	if f.Type == TypeNumber {
		if f.Digits < 1 || f.Digits > 32 {
			return fail(-1, fp+".digits", "digits must be between 1 and 32, got %d", f.Digits)
		}
		if f.Scale < 0 || f.Scale > f.Digits {
			return fail(-1, fp+".scale", "scale must be between 0 and digits (%d), got %d", f.Digits, f.Scale)
		}
	} else if f.Digits != 0 || f.Scale != 0 {
		return fail(-1, fp, "digits and scale are only valid for type number")
	}
	if f.Required && f.Type != TypeRef {
		return fail(-1, fp+".required", "required is only supported for type ref (other types are always required)")
	}
	if f.Positive && f.Type != TypeNumber {
		return fail(-1, fp+".positive", "positive is only supported for type number")
	}
	if f.Unique && f.Type != TypeString {
		return fail(-1, fp+".unique", "unique is only supported for type string")
	}
	if f.Type == TypeRef {
		if f.Target == "" {
			return fail(-1, fp+".target", "target is required for type ref")
		}
		if !catalogs[f.Target] {
			return fail(-1, fp+".target", "broken reference: no catalog with id %q", f.Target)
		}
	} else if f.Target != "" {
		return fail(-1, fp, "target is only valid for type ref")
	}
	return nil
}

// Store держит действующую модель. Неудачная загрузка её не меняет.
type Store struct {
	mu  sync.Mutex
	cur *Model
}

// Load загружает каталог и при успехе делает модель действующей.
func (s *Store) Load(dir string) error {
	m, err := Load(dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cur = m
	s.mu.Unlock()
	return nil
}

// Current возвращает действующую модель или nil, если ничего не загружено.
func (s *Store) Current() *Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// TypeString печатает тип поля с параметрами: string, number(15,3), ref(goods).
func (f Field) TypeString() string {
	switch f.Type {
	case TypeNumber:
		return fmt.Sprintf("number(%d,%d)", f.Digits, f.Scale)
	case TypeRef:
		return fmt.Sprintf("ref(%s)", f.Target)
	}
	return string(f.Type)
}
