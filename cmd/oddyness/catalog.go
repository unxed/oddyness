package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

const catalogUsage = `usage:
  oddyness db create <config-dir> <db-file>
  oddyness db backup <config-dir> <db-file> <backup-file>
  oddyness db restore <config-dir> <backup-file> <new-db-file>
  oddyness catalog add <config-dir> <db-file> <catalog> <field>=<value>...
  oddyness catalog get <config-dir> <db-file> <catalog> <id>
  oddyness catalog list <config-dir> <db-file> <catalog>`

// runDB выполняет `oddyness db create|backup|restore`. backup снимает консистентную копию базы (база открывается как
// обычная сессия: занятая другим процессом отклоняется); restore создаёт новую базу из копии после проверки версии
// схемы, конфигурации и целостности. Существующий файл ни одна команда не перезаписывает.
func runDB(args []string, stdout, stderr io.Writer) int {
	need := map[string]int{"create": 3, "backup": 4, "restore": 4}
	if len(args) == 0 || need[args[0]] == 0 || len(args) != need[args[0]] {
		fmt.Fprintln(stderr, catalogUsage)
		return 2
	}
	m, err := metadata.Load(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	switch args[0] {
	case "backup":
		s, err := openSession(args[2], m)
		if err != nil {
			return fail(err)
		}
		defer s.Close()
		if err := s.Backup(context.Background(), args[3]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "%s: backup written\n", args[3])
		return 0
	case "restore":
		if err := sqlite.Restore(context.Background(), args[2], args[3], m); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "%s: restored\n", args[2])
		return 0
	}
	s, err := sqlite.Create(args[2], m)
	if err != nil {
		return fail(err)
	}
	if err := s.Close(); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "%s: created\n", args[2])
	return 0
}

// runCatalog выполняет команды работы со справочником. Каждая команда — одна
// сессия: база открывается, действие выполняется, база закрывается.
func runCatalog(args []string, stdout, stderr io.Writer) int {
	need := map[string]int{"add": 5, "get": 5, "list": 4}
	if len(args) == 0 || len(args) < need[args[0]] || need[args[0]] == 0 || (args[0] != "add" && len(args) != need[args[0]]) {
		fmt.Fprintln(stderr, catalogUsage)
		return 2
	}
	m, err := metadata.Load(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	c, err := findCatalog(m, args[3])
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	s, err := openSession(args[2], m)
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	defer s.Close()
	ctx := context.Background()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	switch args[0] {
	case "add":
		values, err := parseAssignments(c, args[4:])
		if err != nil {
			return fail(err)
		}
		id, err := s.Insert(ctx, c.ID, values)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "id %d\n", id)
	case "get":
		id, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return fail(fmt.Errorf("invalid id %q", args[4]))
		}
		rec, err := s.Get(ctx, c.ID, id)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, formatRecord(c, rec))
	case "list":
		recs, err := s.List(ctx, c.ID)
		if err != nil {
			return fail(err)
		}
		for _, rec := range recs {
			fmt.Fprintln(stdout, formatRecord(c, rec))
		}
	}
	return 0
}

func findCatalog(m *metadata.Model, key string) (*metadata.Catalog, error) {
	for i := range m.Catalogs {
		c := &m.Catalogs[i]
		if c.ID == key || strings.EqualFold(c.Name, key) {
			return c, nil
		}
	}
	return nil, fmt.Errorf("unknown catalog %q", key)
}

func findField(c *metadata.Catalog, key string) (metadata.Field, error) {
	return findFieldIn(fmt.Sprintf("catalog %q", c.Name), c.Fields, key)
}

func findFieldIn(owner string, fields []metadata.Field, key string) (metadata.Field, error) {
	for _, f := range fields {
		if f.ID == key || strings.EqualFold(f.Name, key) {
			return f, nil
		}
	}
	return metadata.Field{}, fmt.Errorf("%s has no field %q", owner, key)
}

// parseAssignments разбирает `поле=значение` по типам полей метаданных.
func parseAssignments(c *metadata.Catalog, args []string) (map[string]any, error) {
	return parseFieldAssignments(fmt.Sprintf("catalog %q", c.Name), c.Fields, args)
}

func parseFieldAssignments(owner string, fields []metadata.Field, args []string) (map[string]any, error) {
	values := map[string]any{}
	for _, a := range args {
		key, text, ok := strings.Cut(a, "=")
		if !ok {
			return nil, fmt.Errorf("invalid assignment %q, want field=value", a)
		}
		f, err := findFieldIn(owner, fields, key)
		if err != nil {
			return nil, err
		}
		if _, dup := values[f.ID]; dup {
			return nil, fmt.Errorf("field %q is set twice", f.Name)
		}
		v, err := parseValue(f, text)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", f.Name, err)
		}
		values[f.ID] = v
	}
	return values, nil
}

func parseValue(f metadata.Field, text string) (any, error) {
	switch f.Type {
	case metadata.TypeBoolean:
		switch text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("invalid boolean %q (true or false)", text)
	case metadata.TypeNumber:
		n, err := value.Parse(text)
		if err != nil {
			return nil, err
		}
		return n, nil
	case metadata.TypeRef:
		if text == "" {
			return nil, nil
		}
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid reference %q (record id or empty)", text)
		}
		return sqlite.Ref{Catalog: f.Target, ID: id}, nil
	}
	return text, nil
}

// formatRecord печатает запись: `id: Поле=значение ...`; строки в кавычках.
func formatRecord(c *metadata.Catalog, rec sqlite.Record) string {
	return formatValues(rec.ID, c.Fields, rec.Values)
}

func formatValues(id int64, fields []metadata.Field, values map[string]any) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d:", id)
	for _, f := range fields {
		sb.WriteByte(' ')
		sb.WriteString(f.Name)
		sb.WriteByte('=')
		switch v := values[f.ID].(type) {
		case string:
			sb.WriteString(strconv.Quote(v))
		case nil:
			sb.WriteByte('-')
		case sqlite.Ref:
			fmt.Fprintf(&sb, "%d", v.ID)
		default:
			fmt.Fprint(&sb, v)
		}
	}
	return sb.String()
}
