// Package formsbackend связывает окна форм (internal/forms) с прикладным ядром: хранилищем и платформой. Здесь нет
// правил — проверки, отказы и ошибки принадлежат ядру, окно их только показывает.
package formsbackend

import (
	"context"
	"fmt"

	"github.com/unxed/oddyness/internal/forms"
	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/storage/sqlite"
)

// Documents — forms.DocBackend одного вида документа: запись — хранилище, проведение и отмена — платформа
// (обработчики BSL из конфигурации). Поддерживается вид не более чем с одной табличной частью, как и forms.Documents.
type Documents struct {
	store *sqlite.Store
	pl    *platform.Platform
	doc   *metadata.Document
}

// NewDocuments возвращает backend вида документа; неизвестный вид или несколько табличных частей — ошибка.
func NewDocuments(store *sqlite.Store, pl *platform.Platform, docID string) (*Documents, error) {
	for i := range store.Model().Documents {
		d := &store.Model().Documents[i]
		if d.ID == docID {
			if len(d.Tables) > 1 {
				return nil, fmt.Errorf("formsbackend: document %q has %d table parts; at most one is supported", docID, len(d.Tables))
			}
			return &Documents{store: store, pl: pl, doc: d}, nil
		}
	}
	return nil, fmt.Errorf("formsbackend: unknown document %q", docID)
}

func (b *Documents) tables(lines []map[string]any) map[string][]map[string]any {
	if len(b.doc.Tables) == 0 {
		return nil
	}
	return map[string][]map[string]any{b.doc.Tables[0].ID: lines}
}

// List возвращает документы вида по возрастанию номера.
func (b *Documents) List() ([]forms.DocRecord, error) {
	docs, err := b.store.ListDocuments(context.Background(), b.doc.ID)
	if err != nil {
		return nil, err
	}
	out := make([]forms.DocRecord, len(docs))
	for i, doc := range docs {
		rec := forms.DocRecord{ID: doc.ID, Posted: doc.Posted, Header: doc.Header}
		if len(b.doc.Tables) == 1 {
			for _, line := range doc.Tables[b.doc.Tables[0].ID] {
				rec.Lines = append(rec.Lines, line.Values)
			}
		}
		out[i] = rec
	}
	return out, nil
}

// Insert записывает новый документ целиком.
func (b *Documents) Insert(header map[string]any, lines []map[string]any) (int64, error) {
	return b.store.InsertDocument(context.Background(), b.doc.ID, header, b.tables(lines))
}

// Update заменяет шапку и строки черновика; проведённый документ ядро не меняет.
func (b *Documents) Update(id int64, header map[string]any, lines []map[string]any) error {
	return b.store.UpdateDocument(context.Background(), b.doc.ID, id, header, b.tables(lines))
}

// Post проводит документ обработчиком из метаданных.
func (b *Documents) Post(id int64) error { return b.pl.Post(context.Background(), b.doc.ID, id) }

// Unpost отменяет проведение.
func (b *Documents) Unpost(id int64) error { return b.pl.Unpost(context.Background(), b.doc.ID, id) }
