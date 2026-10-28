package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
)

// Схема базы — структурная часть конфигурации: объекты и поля с идентификаторами, типами, точностью, ссылками и
// уникальностью. Она записывается в базу при создании; Open сверяет с ней текущую конфигурацию. Подписи (name),
// признаки проверки ввода (required, positive), обработчики проведения и отчёты в схему не входят: их можно менять,
// данные не страдают. Любое структурное различие (другой тип, удалённое или новое поле, объект, ссылка, точность,
// уникальность) отклоняется до записи — миграций пока нет.

func fieldLine(f metadata.Field) string {
	return fmt.Sprintf("field %s type=%s digits=%d scale=%d target=%s unique=%t", f.ID, f.Type, f.Digits, f.Scale, f.Target, f.Unique)
}

// schemaLines — каноническое описание схемы: отсортированные строки (порядок описания полей данные не затрагивает).
func schemaLines(m *metadata.Model) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	for _, c := range m.Catalogs {
		add("catalog %s", c.ID)
		for _, f := range c.Fields {
			add("catalog %s %s", c.ID, fieldLine(f))
		}
	}
	for _, d := range m.Documents {
		add("document %s", d.ID)
		for _, f := range d.Fields {
			add("document %s %s", d.ID, fieldLine(f))
		}
		for _, t := range d.Tables {
			add("table %s.%s", d.ID, t.ID)
			for _, f := range t.Fields {
				add("table %s.%s %s", d.ID, t.ID, fieldLine(f))
			}
		}
	}
	for _, r := range m.Registers {
		add("register %s", r.ID)
		for _, f := range r.Dimensions {
			add("register %s dimension %s", r.ID, fieldLine(f))
		}
		for _, f := range r.Resources {
			add("register %s resource %s", r.ID, fieldLine(f))
		}
	}
	sort.Strings(out)
	return out
}

// schemaFingerprint — отпечаток схемы (хеш канонических строк).
func schemaFingerprint(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// schemaDiff описывает различие сохранённой и текущей схемы: «-» есть в базе и отсутствует (или изменено) в
// конфигурации, «+» есть в конфигурации и отсутствует (или изменено) в базе.
func schemaDiff(stored, current []string) []string {
	in := func(list []string) map[string]bool {
		m := make(map[string]bool, len(list))
		for _, l := range list {
			m[l] = true
		}
		return m
	}
	st, cur := in(stored), in(current)
	var out []string
	for _, l := range stored {
		if !cur[l] {
			out = append(out, "- "+l)
		}
	}
	for _, l := range current {
		if !st[l] {
			out = append(out, "+ "+l)
		}
	}
	return out
}
