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
  oddyness catalog add <config-dir> <db-file> <catalog> <field>=<value>...
  oddyness catalog get <config-dir> <db-file> <catalog> <id>
  oddyness catalog list <config-dir> <db-file> <catalog>`

// runDB выполняет `oddyness db create <config-dir> <db-file>`.
func runDB(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 || args[0] != "create" {
		fmt.Fprintln(stderr, catalogUsage)
		return 2
	}
	m, err := metadata.Load(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	s, err := sqlite.Create(args[2], m)
	if err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
	}
	if err := s.Close(); err != nil {
		fmt.Fprintf(stderr, "oddyness: %v\n", err)
		return 1
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
	s, err := sqlite.Open(args[2], m)
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
	for _, f := range c.Fields {
		if f.ID == key || strings.EqualFold(f.Name, key) {
			return f, nil
		}
	}
	return metadata.Field{}, fmt.Errorf("catalog %q has no field %q", c.Name, key)
}

// parseAssignments разбирает `поле=значение` по типам полей метаданных.
func parseAssignments(c *metadata.Catalog, args []string) (map[string]any, error) {
	values := map[string]any{}
	for _, a := range args {
		key, text, ok := strings.Cut(a, "=")
		if !ok {
			return nil, fmt.Errorf("invalid assignment %q, want field=value", a)
		}
		f, err := findField(c, key)
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
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d:", rec.ID)
	for _, f := range c.Fields {
		sb.WriteByte(' ')
		sb.WriteString(f.Name)
		sb.WriteByte('=')
		switch v := rec.Values[f.ID].(type) {
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
