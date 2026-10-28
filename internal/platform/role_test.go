package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/unxed/oddyness/internal/storage/sqlite"
)

// S11 через API платформы и BSL host API: под ролью чтения проведение и отмена отклоняются до выполнения обработчика
// (BSL не запускается и не может вызвать Движение), статус и движения не меняются, чтение остатков разрешено;
// роль оператора возвращает проведение.
func TestReaderRoleS11(t *testing.T) {
	e := newEnv(t)
	pl := e.platform(Options{})
	posted := e.receipt("R-1", [2]any{1, "10.500"})
	if err := pl.Post(ctx, "receipt", posted); err != nil {
		t.Fatal(err)
	}
	draft := e.receipt("R-2", [2]any{2, "3.000"})
	want := map[int64]string{1: "10.500"}
	eq(t, e.balances(), want)

	e.store.SetRole(sqlite.RoleReader)
	for name, err := range map[string]error{
		"Post of a draft":  pl.Post(ctx, "receipt", draft),
		"Post of a posted": pl.Post(ctx, "receipt", posted),
		"Unpost":           pl.Unpost(ctx, "receipt", posted),
	} {
		if !errors.Is(err, sqlite.ErrDenied) || !strings.Contains(err.Error(), "role read") {
			t.Errorf("%s under the read role: %v", name, err)
		}
	}
	// обработчик, который отказал бы сообщением, не выполняется: отказ приходит от роли, а не от BSL
	rej, err := e.withHandler("Процедура ПриПроведении(Документ)\n\tОтказ(\"обработчик выполнился\");\nКонецПроцедуры\n", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rej.Post(ctx, "receipt", draft); !errors.Is(err, sqlite.ErrDenied) || errors.Is(err, ErrRejected) || strings.Contains(err.Error(), "обработчик") {
		t.Errorf("a handler ran under the read role: %v", err)
	}
	// записать движения напрямую (host API готовит их только внутри Post) нельзя и мимо платформы
	if err := e.store.ReplaceMovements(ctx, "stock", "receipt", draft, []map[string]any{{"stock-product": sqlite.Ref{Catalog: "goods", ID: 2}, "stock-qty": e.num("3.000")}}); !errors.Is(err, sqlite.ErrDenied) {
		t.Errorf("ReplaceMovements under the read role: %v", err)
	}
	// ничего не изменилось; чтение разрешено
	eq(t, e.balances(), want)
	if d, err := e.store.GetDocument(ctx, "receipt", draft); err != nil || d.Posted || e.movementCount(draft) != 0 {
		t.Errorf("the draft changed: posted=%v movements=%d err=%v", d.Posted, e.movementCount(draft), err)
	}
	if d, err := e.store.GetDocument(ctx, "receipt", posted); err != nil || !d.Posted || e.movementCount(posted) != 1 {
		t.Errorf("the posted receipt changed: posted=%v movements=%d err=%v", d.Posted, e.movementCount(posted), err)
	}

	e.store.SetRole(sqlite.RoleOperator)
	if err := pl.Post(ctx, "receipt", draft); err != nil {
		t.Fatalf("the operator role cannot post: %v", err)
	}
	eq(t, e.balances(), map[int64]string{1: "10.500", 2: "3.000"})
}
