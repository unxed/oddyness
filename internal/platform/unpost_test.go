package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/storage/sqlite"
)

func (e *env) withUnposting(src string) *Platform {
	e.t.Helper()
	dir := e.t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(smallStock)); err != nil {
		e.t.Fatal(err)
	}
	f := filepath.Join(dir, "bsl", "receipt-unposting.bsl")
	if err := os.Chmod(f, 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(src), 0o600); err != nil {
		e.t.Fatal(err)
	}
	p, err := Open(e.store, dir, Options{})
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

// S06: отменить выдачу и провести снова — последовательно 10,500 и 8,250; после отмены документ — черновик.
func TestUnpostS06(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	if err := pl.Post(ctx, "receipt", e.receipt("R-1", [2]any{1, "10.500"})); err != nil {
		t.Fatal(err)
	}
	id := e.issue("I-1", [2]any{1, "2.250"})
	if err := pl.Post(ctx, "issue", id); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "8.250"})
	if err := pl.Unpost(ctx, "issue", id); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.500"})
	if posted, n := e.issueState(id); posted || n != 0 {
		t.Errorf("after cancel: posted=%v movements=%d", posted, n)
	}
	// черновик снова можно править
	err := e.store.UpdateDocument(ctx, "issue", id, map[string]any{"issue-number": "I-1", "issue-date": "2026-10-28", "issue-recipient": "склад"},
		map[string][]map[string]any{"issue-items": {{"issue-product": sqlite.Ref{Catalog: "goods", ID: 1}, "issue-qty": e.num("2.250")}}})
	if err != nil {
		t.Fatalf("update after cancel: %v", err)
	}
	if err := pl.Post(ctx, "issue", id); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{1: "8.250"})
	// перезапуск: статус и остаток сохранены
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(e.path, e.model)
	if err != nil {
		t.Fatal(err)
	}
	e.store = store
	t.Cleanup(func() { store.Close() })
	eq(t, e.balances(), map[int64]string{1: "8.250"})
	if posted, n := e.issueState(id); !posted || n != 1 {
		t.Errorf("after restart: posted=%v movements=%d", posted, n)
	}
}

// S07: отмена поступления после выдачи отклоняется; статус и движения обоих документов неизменны.
// Отмена непроведённого документа и повтор отмены — явная ошибка.
func TestUnpostS07AndRepeat(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	rid := e.receipt("R-1", [2]any{1, "10.500"})
	if err := pl.Post(ctx, "receipt", rid); err != nil {
		t.Fatal(err)
	}
	iid := e.issue("I-1", [2]any{1, "2.250"})
	if err := pl.Post(ctx, "issue", iid); err != nil {
		t.Fatal(err)
	}
	err := pl.Unpost(ctx, "receipt", rid)
	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "отрицательный остаток") {
		t.Fatalf("cancel of the receipt after the issue: %v", err)
	}
	eq(t, e.balances(), map[int64]string{1: "8.250"})
	if !e.posted(rid) || e.movementCount(rid) != 1 {
		t.Errorf("receipt changed: posted=%v movements=%d", e.posted(rid), e.movementCount(rid))
	}
	if posted, n := e.issueState(iid); !posted || n != 1 {
		t.Errorf("issue changed: posted=%v movements=%d", posted, n)
	}
	// после отмены выдачи поступление отменить можно, повтор — ошибка
	if err := pl.Unpost(ctx, "issue", iid); err != nil {
		t.Fatal(err)
	}
	if err := pl.Unpost(ctx, "receipt", rid); err != nil {
		t.Fatal(err)
	}
	eq(t, e.balances(), map[int64]string{})
	if err := pl.Unpost(ctx, "receipt", rid); !errors.Is(err, ErrNotPosted) {
		t.Errorf("repeated cancel: %v", err)
	}
	if err := pl.Unpost(ctx, "receipt", e.receipt("R-2", [2]any{1, "1"})); !errors.Is(err, ErrNotPosted) {
		t.Errorf("cancel of a draft: %v", err)
	}
	if err := pl.Unpost(ctx, "receipt", 99); !errors.Is(err, sqlite.ErrNotFound) {
		t.Errorf("unknown document: %v", err)
	}
	if err := pl.Unpost(ctx, "nope", 1); !errors.Is(err, ErrNoHandler) {
		t.Errorf("unknown kind: %v", err)
	}
}

// Отмена поступления, не доводящая остаток до отрицательного, проходит; остаток возвращается.
func TestUnpostKeepsOtherReceipts(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	r1 := e.receipt("R-1", [2]any{1, "10.500"})
	r2 := e.receipt("R-2", [2]any{1, "5.000"})
	for _, id := range []int64{r1, r2} {
		if err := pl.Post(ctx, "receipt", id); err != nil {
			t.Fatal(err)
		}
	}
	if err := pl.Post(ctx, "issue", e.issue("I-1", [2]any{1, "12.000"})); err != nil {
		t.Fatal(err)
	}
	if err := pl.Unpost(ctx, "receipt", r2); !errors.Is(err, ErrRejected) {
		t.Fatalf("cancel leaving -2.000: %v", err)
	}
	eq(t, e.balances(), map[int64]string{1: "3.500"})
}

// Ошибка обработчика отмены (лимит инструкций) откатывает всё; обработчик не может готовить движения.
func TestUnpostErrorsRollBack(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	rid := e.receipt("R-1", [2]any{1, "10.500"})
	if err := pl.Post(ctx, "receipt", rid); err != nil {
		t.Fatal(err)
	}
	slow := e.platform(Options{MaxSteps: 5})
	if err := slow.Unpost(ctx, "receipt", rid); err == nil || errors.Is(err, ErrRejected) {
		t.Fatalf("step limit: %v", err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.500"})
	if !e.posted(rid) || e.movementCount(rid) != 1 {
		t.Errorf("state changed after a failed cancel: posted=%v movements=%d", e.posted(rid), e.movementCount(rid))
	}
	bad := e.withUnposting("Процедура ПриОтменеПроведения(Документ)\n\tДвижение(\"Запасы\", Неопределено, 1);\nКонецПроцедуры\n")
	if err := bad.Unpost(ctx, "receipt", rid); err == nil || !strings.Contains(err.Error(), "cannot be prepared") {
		t.Fatalf("movement in the cancel handler: %v", err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.500"})
	if !e.posted(rid) {
		t.Error("status changed")
	}
}
