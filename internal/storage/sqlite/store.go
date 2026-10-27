// Package sqlite хранит справочники в SQLite без CGO (modernc.org/sqlite).
//
// База создаётся из метаданных (internal/metadata): по таблице на справочник,
// типизированные ссылки — внешние ключи, уникальные поля — уникальные индексы.
// Открытая база принадлежит одной локальной сессии: соединение единственное и
// держит эксклюзивную блокировку файла, вторая сессия получает ErrLocked, а
// блокировка снимается закрытием или завершением процесса. Базу другой версии
// схемы или созданную по другой конфигурации Open отвергает и не меняет.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // драйвер database/sql "sqlite"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/value"
)

// SchemaVersion — версия схемы хранилища (PRAGMA user_version и таблица метаданных).
const SchemaVersion = 1

var (
	// ErrLocked — база занята другой сессией.
	ErrLocked = errors.New("sqlite: database is locked by another session")
	// ErrVersion — версия схемы базы не совпадает с поддерживаемой.
	ErrVersion = errors.New("sqlite: unsupported database schema version")
	// ErrConfig — база создана по другой конфигурации.
	ErrConfig = errors.New("sqlite: database was created for a different configuration")
	// ErrNotFound — записи с таким идентификатором нет.
	ErrNotFound = errors.New("sqlite: record not found")
	// ErrDuplicate — нарушена уникальность поля.
	ErrDuplicate = errors.New("sqlite: duplicate value of a unique field")
	// ErrInvalid — значения не соответствуют метаданным (неизвестное поле,
	// пропущенное поле, неверный тип, число не помещается в digits/scale,
	// ссылка на другой справочник или на несуществующую запись).
	ErrInvalid = errors.New("sqlite: invalid value")
	// ErrUnknown — неизвестный справочник.
	ErrUnknown = errors.New("sqlite: unknown catalog")
	// ErrExists — файл базы уже существует (Create ничего не перезаписывает).
	ErrExists = errors.New("sqlite: database file already exists")
)

// Ref — типизированная ссылка на запись справочника (value.Ref).
type Ref = value.Ref

// Record — запись справочника; Values индексированы по id поля метаданных.
// Типы значений: string, bool, value.Number, Ref (или nil для пустой ссылки).
type Record struct {
	ID     int64
	Values map[string]any
}

// Store — открытая база одной локальной сессии.
type Store struct {
	db       *sql.DB
	model    *metadata.Model
	catalogs map[string]*metadata.Catalog
}

func tableName(catalogID string) string { return `"cat_` + catalogID + `"` }

// docTable и partTable — таблицы шапок и табличных частей документов. Точка в
// имени таблицы допустима в кавычках и не бывает в идентификаторах метаданных,
// поэтому имена не пересекаются.
func docTable(docID string) string           { return `"doc_` + docID + `"` }
func partTable(docID, tableID string) string { return `"doc_` + docID + `.` + tableID + `"` }

// columnDefs возвращает определения столбцов полей, внешние ключи и уникальные
// индексы таблицы table; prefix — часть имени индекса.
func columnDefs(prefix, table string, fields []metadata.Field) (cols, tail, indexes []string) {
	for _, f := range fields {
		col := columnName(f.ID)
		switch f.Type {
		case metadata.TypeBoolean:
			cols = append(cols, col+` INTEGER NOT NULL CHECK (`+col+` IN (0, 1))`)
		case metadata.TypeRef:
			cols = append(cols, col+` INTEGER`)
			tail = append(tail, fmt.Sprintf(`FOREIGN KEY (%s) REFERENCES %s("id")`, col, tableName(f.Target)))
		default: // string, number, date: текст; число хранится точной десятичной записью
			cols = append(cols, col+` TEXT NOT NULL`)
		}
		if f.Unique {
			indexes = append(indexes, fmt.Sprintf(`CREATE UNIQUE INDEX "u_%s_%s" ON %s(%s)`, prefix, f.ID, table, col))
		}
	}
	return cols, tail, indexes
}
func columnName(fieldID string) string { return `"f_` + fieldID + `"` }

// fingerprint — хеш канонической записи модели: база привязана к конфигурации.
func fingerprint(m *metadata.Model) (string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func dsn(path string) (string, error) {
	if strings.ContainsAny(path, "?#") {
		return "", fmt.Errorf("sqlite: unsupported character in database path %q", path)
	}
	return path + "?_pragma=busy_timeout(0)&_pragma=foreign_keys(1)&_pragma=journal_mode(DELETE)&_pragma=locking_mode(EXCLUSIVE)", nil
}

// connect открывает единственное соединение и берёт эксклюзивную блокировку.
func connect(path string) (*sql.DB, error) {
	d, err := dsn(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", d)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	// BEGIN EXCLUSIVE берёт эксклюзивную блокировку файла; в режиме locking_mode=EXCLUSIVE она не снимается до закрытия.
	if _, err := db.Exec("BEGIN EXCLUSIVE; COMMIT"); err != nil {
		db.Close()
		if isBusy(err) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	return db, nil
}

// isBusy узнаёт SQLITE_BUSY/SQLITE_LOCKED по тексту ошибки драйвера: числовой
// код доступен через sqlite.Error, но текст не зависит от расширенных кодов.
func isBusy(err error) bool {
	s := err.Error()
	return strings.Contains(s, "database is locked") || strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "SQLITE_LOCKED")
}

// Create создаёт новую базу по модели. Существующий файл не трогается.
// При ошибке недосозданный файл удаляется.
func Create(path string, m *metadata.Model) (*Store, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	fp, err := fingerprint(m)
	if err != nil {
		return nil, err
	}
	db, err := connect(path)
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	s := newStore(db, m)
	if err := s.createSchema(fp); err != nil {
		db.Close()
		os.Remove(path)
		return nil, err
	}
	return s, nil
}

func newStore(db *sql.DB, m *metadata.Model) *Store {
	s := &Store{db: db, model: m, catalogs: map[string]*metadata.Catalog{}}
	for i := range m.Catalogs {
		s.catalogs[m.Catalogs[i].ID] = &m.Catalogs[i]
	}
	return s
}

func (s *Store) createSchema(fp string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{`CREATE TABLE oddyness_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`}
	for _, c := range s.model.Catalogs {
		cols, tail, indexes := columnDefs("cat_"+c.ID, tableName(c.ID), c.Fields)
		stmts = append(stmts, fmt.Sprintf(`CREATE TABLE %s (%s)`, tableName(c.ID), strings.Join(append([]string{`"id" INTEGER PRIMARY KEY`}, append(cols, tail...)...), ", ")))
		stmts = append(stmts, indexes...)
	}
	for _, d := range s.model.Documents {
		cols, tail, indexes := columnDefs("doc_"+d.ID, docTable(d.ID), d.Fields)
		docHead := []string{`"id" INTEGER PRIMARY KEY`, `"posted" INTEGER NOT NULL DEFAULT 0 CHECK ("posted" IN (0, 1))`}
		stmts = append(stmts, fmt.Sprintf(`CREATE TABLE %s (%s)`, docTable(d.ID), strings.Join(append(docHead, append(cols, tail...)...), ", ")))
		stmts = append(stmts, indexes...)
		for _, t := range d.Tables {
			name := partTable(d.ID, t.ID)
			cols, tail, indexes := columnDefs("part_"+d.ID+"_"+t.ID, name, t.Fields)
			head := []string{`"id" INTEGER PRIMARY KEY`, `"owner" INTEGER NOT NULL REFERENCES ` + docTable(d.ID) + `("id") ON DELETE CASCADE`, `"line" INTEGER NOT NULL`}
			tail = append(tail, `UNIQUE ("owner", "line")`)
			stmts = append(stmts, fmt.Sprintf(`CREATE TABLE %s (%s)`, name, strings.Join(append(head, append(cols, tail...)...), ", ")))
			stmts = append(stmts, indexes...)
		}
	}
	for _, r := range s.model.Registers {
		fields := regFields(&r)
		cols, tail, indexes := columnDefs("reg_"+r.ID, regTable(r.ID), fields)
		head := []string{`"id" INTEGER PRIMARY KEY`, `"recorder_doc" TEXT NOT NULL`, `"recorder_id" INTEGER NOT NULL`, `"line" INTEGER NOT NULL`}
		tail = append(tail, `UNIQUE ("recorder_doc", "recorder_id", "line")`)
		stmts = append(stmts, fmt.Sprintf(`CREATE TABLE %s (%s)`, regTable(r.ID), strings.Join(append(head, append(cols, tail...)...), ", ")))
		stmts = append(stmts, indexes...)
		stmts = append(stmts, fmt.Sprintf(`CREATE INDEX "r_%s_recorder" ON %s ("recorder_doc", "recorder_id")`, r.ID, regTable(r.ID)))
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("sqlite: schema: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO oddyness_meta (key, value) VALUES ('schema_version', ?), ('fingerprint', ?)`, fmt.Sprint(SchemaVersion), fp); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// Open открывает существующую базу. Версия схемы и конфигурация проверяются до
// любого изменения; несовпадение — ErrVersion или ErrConfig, база остаётся как была.
func Open(path string, m *metadata.Model) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	fp, err := fingerprint(m)
	if err != nil {
		return nil, err
	}
	db, err := connect(path)
	if err != nil {
		return nil, err
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	if version != SchemaVersion {
		db.Close()
		return nil, fmt.Errorf("%w: found %d, supported %d", ErrVersion, version, SchemaVersion)
	}
	var stored string
	if err := db.QueryRow(`SELECT value FROM oddyness_meta WHERE key = 'fingerprint'`).Scan(&stored); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: not an oddyness database: %w", err)
	}
	if stored != fp {
		db.Close()
		return nil, ErrConfig
	}
	return newStore(db, m), nil
}

// Close закрывает базу и снимает блокировку сессии.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) catalog(id string) (*metadata.Catalog, error) {
	c, ok := s.catalogs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknown, id)
	}
	return c, nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// encode проверяет значение по полю метаданных и приводит его к значению столбца.
func encode(f metadata.Field, v any) (any, error) {
	switch f.Type {
	case metadata.TypeString:
		str, ok := v.(string)
		if !ok {
			return nil, invalid("field %q: expected string, got %T", f.ID, v)
		}
		return str, nil
	case metadata.TypeBoolean:
		b, ok := v.(bool)
		if !ok {
			return nil, invalid("field %q: expected bool, got %T", f.ID, v)
		}
		if b {
			return 1, nil
		}
		return 0, nil
	case metadata.TypeNumber:
		n, ok := v.(value.Number)
		if !ok {
			return nil, invalid("field %q: expected value.Number, got %T", f.ID, v)
		}
		if err := n.CheckPrecision(f.Digits, f.Scale); err != nil {
			return nil, invalid("field %q: %v", f.ID, err)
		}
		if f.Positive && n.Sign() <= 0 {
			return nil, invalid("field %q: %s must be greater than zero", f.ID, n)
		}
		return n.String(), nil
	case metadata.TypeDate:
		str, ok := v.(string)
		if !ok {
			return nil, invalid("field %q: expected a date string, got %T", f.ID, v)
		}
		if _, err := time.Parse("2006-01-02", str); err != nil {
			return nil, invalid("field %q: %q is not a date YYYY-MM-DD", f.ID, str)
		}
		return str, nil
	default: // ref
		if v == nil {
			if f.Required {
				return nil, invalid("field %q: the reference is required", f.ID)
			}
			return nil, nil
		}
		r, ok := v.(Ref)
		if !ok {
			return nil, invalid("field %q: expected Ref or nil, got %T", f.ID, v)
		}
		if r.Catalog != f.Target {
			return nil, invalid("field %q: reference to catalog %q, the field references %q", f.ID, r.Catalog, f.Target)
		}
		return r.ID, nil
	}
}

// encodeAll проверяет, что заданы ровно поля справочника, и кодирует значения
// в порядке полей метаданных.
func encodeAll(c *metadata.Catalog, values map[string]any) ([]any, error) {
	return encodeFields("catalog "+c.ID, c.Fields, values)
}

// encodeFields — то же для произвольного списка полей; owner попадает в сообщения.
func encodeFields(owner string, fields []metadata.Field, values map[string]any) ([]any, error) {
	known := make(map[string]bool, len(fields))
	out := make([]any, 0, len(fields))
	for _, f := range fields {
		known[f.ID] = true
		v, present := values[f.ID]
		if !present && f.Type == metadata.TypeRef && f.Required {
			return nil, invalid("missing value for required reference %q", f.ID)
		}
		if !present {
			if f.Type == metadata.TypeRef {
				out = append(out, nil) // пустая ссылка допустима
				continue
			}
			return nil, invalid("missing value for field %q", f.ID)
		}
		e, err := encode(f, v)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	for id := range values {
		if !known[id] {
			return nil, invalid("unknown field %q in %s", id, owner)
		}
	}
	return out, nil
}

// checkUnique проверяет уникальные поля внутри транзакции; except — запись,
// которую обновляют (0 — новая).
func (s *Store) checkUnique(ctx context.Context, tx *sql.Tx, c *metadata.Catalog, encoded []any, except int64) error {
	return checkUniqueIn(ctx, tx, tableName(c.ID), c.Fields, encoded, except)
}

func checkUniqueIn(ctx context.Context, tx *sql.Tx, table string, fields []metadata.Field, encoded []any, except int64) error {
	for i, f := range fields {
		if !f.Unique {
			continue
		}
		var one int
		err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT 1 FROM %s WHERE %s = ? AND "id" <> ?`, table, columnName(f.ID)), encoded[i], except).Scan(&one)
		switch {
		case err == nil:
			return fmt.Errorf("%w: field %q value %q", ErrDuplicate, f.ID, encoded[i])
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
	}
	return nil
}

// mapErr переводит ошибки SQLite в ошибки пакета: нарушение внешнего ключа
// (ссылка на несуществующую запись или удаление используемой) — ErrInvalid.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "FOREIGN KEY constraint failed"):
		return fmt.Errorf("%w: reference to a missing record or record is in use (%v)", ErrInvalid, err)
	case strings.Contains(s, "UNIQUE constraint failed"):
		return fmt.Errorf("%w: %v", ErrDuplicate, err)
	}
	return err
}

func (s *Store) inTx(ctx context.Context, f func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return mapErr(tx.Commit())
}

// Insert добавляет запись и возвращает её идентификатор. Все поля обязательны,
// кроме ссылок (пустая ссылка — NULL).
func (s *Store) Insert(ctx context.Context, catalogID string, values map[string]any) (int64, error) {
	c, err := s.catalog(catalogID)
	if err != nil {
		return 0, err
	}
	encoded, err := encodeAll(c, values)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := s.checkUnique(ctx, tx, c, encoded, 0); err != nil {
			return err
		}
		cols := make([]string, len(c.Fields))
		marks := make([]string, len(c.Fields))
		for i, f := range c.Fields {
			cols[i], marks[i] = columnName(f.ID), "?"
		}
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, tableName(c.ID), strings.Join(cols, ", "), strings.Join(marks, ", ")), encoded...)
		if err != nil {
			return mapErr(err)
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// Update полностью заменяет значения записи (правила — как у Insert).
func (s *Store) Update(ctx context.Context, catalogID string, id int64, values map[string]any) error {
	c, err := s.catalog(catalogID)
	if err != nil {
		return err
	}
	encoded, err := encodeAll(c, values)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := s.checkUnique(ctx, tx, c, encoded, id); err != nil {
			return err
		}
		sets := make([]string, len(c.Fields))
		for i, f := range c.Fields {
			sets[i] = columnName(f.ID) + " = ?"
		}
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE "id" = ?`, tableName(c.ID), strings.Join(sets, ", ")), append(encoded, id)...)
		if err != nil {
			return mapErr(err)
		}
		return affected(res, catalogID, id)
	})
}

func affected(res sql.Result, catalogID string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s %d", ErrNotFound, catalogID, id)
	}
	return nil
}

// Delete удаляет запись. Запись, на которую есть ссылки, удалить нельзя (ErrInvalid).
func (s *Store) Delete(ctx context.Context, catalogID string, id int64) error {
	c, err := s.catalog(catalogID)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE "id" = ?`, tableName(c.ID)), id)
		if err != nil {
			return mapErr(err)
		}
		return affected(res, catalogID, id)
	})
}

func (s *Store) selectSQL(c *metadata.Catalog) string {
	cols := []string{`"id"`}
	for _, f := range c.Fields {
		cols = append(cols, columnName(f.ID))
	}
	return fmt.Sprintf(`SELECT %s FROM %s`, strings.Join(cols, ", "), tableName(c.ID))
}

func scanRecord(c *metadata.Catalog, row interface{ Scan(...any) error }) (Record, error) {
	return scanFields(c.Fields, row)
}

// scanFields читает строку из столбца идентификатора и столбцов полей.
func scanFields(fields []metadata.Field, row interface{ Scan(...any) error }) (Record, error) {
	raw := make([]any, len(fields)+1)
	var id int64
	raw[0] = &id
	for i, f := range fields {
		switch f.Type {
		case metadata.TypeBoolean:
			raw[i+1] = new(int64)
		case metadata.TypeRef:
			raw[i+1] = new(sql.NullInt64)
		default:
			raw[i+1] = new(string)
		}
	}
	if err := row.Scan(raw...); err != nil {
		return Record{}, err
	}
	rec := Record{ID: id, Values: make(map[string]any, len(fields))}
	for i, f := range fields {
		switch f.Type {
		case metadata.TypeBoolean:
			rec.Values[f.ID] = *raw[i+1].(*int64) == 1
		case metadata.TypeRef:
			if n := raw[i+1].(*sql.NullInt64); n.Valid {
				rec.Values[f.ID] = Ref{Catalog: f.Target, ID: n.Int64}
			} else {
				rec.Values[f.ID] = nil
			}
		case metadata.TypeNumber:
			n, err := value.Parse(*raw[i+1].(*string))
			if err != nil {
				return Record{}, fmt.Errorf("sqlite: corrupt number in field %q: %w", f.ID, err)
			}
			rec.Values[f.ID] = n
		default:
			rec.Values[f.ID] = *raw[i+1].(*string)
		}
	}
	return rec, nil
}

// Get возвращает запись или ErrNotFound.
func (s *Store) Get(ctx context.Context, catalogID string, id int64) (Record, error) {
	c, err := s.catalog(catalogID)
	if err != nil {
		return Record{}, err
	}
	rec, err := scanRecord(c, s.db.QueryRowContext(ctx, s.selectSQL(c)+` WHERE "id" = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("%w: %s %d", ErrNotFound, catalogID, id)
	}
	return rec, err
}

// List возвращает все записи справочника по возрастанию идентификатора.
func (s *Store) List(ctx context.Context, catalogID string) ([]Record, error) {
	c, err := s.catalog(catalogID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, s.selectSQL(c)+` ORDER BY "id"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		rec, err := scanRecord(c, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
