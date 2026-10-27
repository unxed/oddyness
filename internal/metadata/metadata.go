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
)

// Field — поле справочника.
type Field struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Type   FieldType `json:"type"`
	Digits int       `json:"digits,omitempty"`
	Scale  int       `json:"scale,omitempty"`
	Target string    `json:"target,omitempty"`
}

// Catalog — справочник.
type Catalog struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

// Model — загруженная модель.
type Model struct {
	Version  int       `json:"version"`
	Catalogs []Catalog `json:"catalogs"`
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
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fail(int(dec.InputOffset()), "", "unexpected data after the top-level object")
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
	for i, c := range m.Catalogs {
		fieldNames := map[string]string{}
		for j, f := range c.Fields {
			fp := fmt.Sprintf("catalogs[%d].fields[%d]", i, j)
			if err := checkID(fp, f.ID); err != nil {
				return err
			}
			if f.Name == "" {
				return fail(-1, fp+".name", "name is required")
			}
			if first, dup := fieldNames[strings.ToLower(f.Name)]; dup {
				return fail(-1, fp+".name", "duplicate field name %q in catalog %q (first used at %s)", f.Name, c.Name, first)
			}
			fieldNames[strings.ToLower(f.Name)] = fp
			if err := m.validateType(fp, f, catalogs, fail); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Model) validateType(fp string, f Field, catalogs map[string]bool, fail failFunc) error {
	switch f.Type {
	case TypeString, TypeBoolean, TypeNumber, TypeRef:
	default:
		return fail(-1, fp+".type", "unknown type %q (supported: string, boolean, number, ref)", f.Type)
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
