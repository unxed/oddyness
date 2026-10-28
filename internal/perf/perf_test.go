// Пакет perf — повторяемый набор товаров и документов, измерения проведения и запроса и проверка правильности остатков
// при росте объёма (P24). Только тесты: набор строится детерминированно (docs/implementation/performance.md).
package perf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/unxed/oddyness/internal/metadata"
	"github.com/unxed/oddyness/internal/platform"
	"github.com/unxed/oddyness/internal/query"
	"github.com/unxed/oddyness/internal/storage/sqlite"
	"github.com/unxed/oddyness/internal/value"
)

var ctx = context.Background()

// Размеры набора: товары, поступления (строк в каждом) и выдачи (строк в каждой).
const (
	goodsN       = 100
	receiptsN    = 200
	receiptLines = 20
	issuesN      = 100
	issueLines   = 10
)

const stockQuery = "ВЫБРАТЬ Товар, Количество КАК Остаток ИЗ РегистрНакопления.Запасы.Остатки УПОРЯДОЧИТЬ ПО Товар"

type fixture struct {
	t        testing.TB
	model    *metadata.Model
	store    *sqlite.Store
	pl       *platform.Platform
	expected map[int64]value.Number // ожидаемые остатки, посчитанные независимо от ядра
	posted   int
}

func num(t testing.TB, s string) value.Number {
	t.Helper()
	n, err := value.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func product(id int64) value.Ref { return value.Ref{Catalog: "goods", ID: id} }

func receiptQty(d, j int) string { return fmt.Sprintf("%d.%03d", (d+j)%9+1, (d*37+j*11)%1000) }
func issueQty(d, j int) string   { return fmt.Sprintf("0.%03d", (d+j)%5+1) }

// newFixture создаёт пустую базу «Малого склада» с товарами; документы добавляют receipt и issue.
func newFixture(t testing.TB) *fixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../testdata/configs/small-stock")); err != nil {
		t.Fatal(err)
	}
	model, err := metadata.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Create(filepath.Join(t.TempDir(), "db.sqlite"), model)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for i := 1; i <= goodsN; i++ {
		if _, err := store.Insert(ctx, "goods", map[string]any{"goods-code": fmt.Sprintf("G-%03d", i), "goods-title": "товар", "goods-unit": "шт", "goods-active": true, "goods-min": num(t, "0")}); err != nil {
			t.Fatal(err)
		}
	}
	pl, err := platform.Open(store, dir, platform.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, model: model, store: store, pl: pl, expected: map[int64]value.Number{}}
}

// add записывает документ вида kind (receipt или issue) с номером d и строками; при post проводит его.
func (f *fixture) add(kind string, d int, post bool) int64 {
	f.t.Helper()
	lines, qty, other := receiptLines, receiptQty, "receipt-comment"
	pid := func(j int) int64 { return int64((d*7+j)%goodsN + 1) }
	if kind == "issue" {
		lines, qty, other = issueLines, issueQty, "issue-recipient"
		pid = func(j int) int64 { return int64((d*3+j)%goodsN + 1) }
	}
	rows := make([]map[string]any, lines)
	for j := range rows {
		rows[j] = map[string]any{kind + "-product": product(pid(j)), kind + "-qty": num(f.t, qty(d, j))}
	}
	id, err := f.store.InsertDocument(ctx, kind, map[string]any{kind + "-number": fmt.Sprintf("%s-%d", kind, d), kind + "-date": "2026-10-28", other: "x"},
		map[string][]map[string]any{kind + "-items": rows})
	if err != nil {
		f.t.Fatal(err)
	}
	if post {
		if err := f.pl.Post(ctx, kind, id); err != nil {
			f.t.Fatalf("post %s %d: %v", kind, d, err)
		}
		f.posted++
		for j := range rows {
			cur := f.expected[pid(j)]
			q := num(f.t, qty(d, j))
			var next value.Number
			if kind == "receipt" {
				next, err = cur.Add(q)
			} else {
				next, err = cur.Sub(q)
			}
			if err != nil {
				f.t.Fatal(err)
			}
			f.expected[pid(j)] = next
		}
	}
	return id
}

// fill проводит весь набор: поступления, затем выдачи.
func (f *fixture) fill() {
	for d := 0; d < receiptsN; d++ {
		f.add("receipt", d, true)
	}
	for d := 0; d < issuesN; d++ {
		f.add("issue", d, true)
	}
}

func (f *fixture) balances() map[int64]value.Number {
	f.t.Helper()
	bs, err := f.store.Balances(ctx, "stock", sqlite.BalanceQuery{})
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[int64]value.Number{}
	for _, b := range bs {
		out[b.Dimensions["stock-product"].(sqlite.Ref).ID] = b.Resources["stock-qty"]
	}
	return out
}

// check сверяет остатки ядра с независимо посчитанными (сравнение точное, по значению).
func (f *fixture) check(got map[int64]value.Number) {
	f.t.Helper()
	for id, want := range f.expected {
		g, ok := got[id]
		if !ok && want.Cmp(num(f.t, "0")) == 0 {
			continue
		}
		if !ok || g.Cmp(want) != 0 {
			f.t.Fatalf("product %d: balance %v, want %v", id, g, want)
		}
	}
	for id := range got {
		if _, ok := f.expected[id]; !ok {
			f.t.Fatalf("product %d has a balance %v but no movements were expected", id, got[id])
		}
	}
}

// Правильность остатков при росте объёма: набор проводится целиком, остатки ядра и запроса равны независимому подсчёту,
// условия измерения и времена выводятся в лог (команда повтора — в docs/implementation/performance.md).
func TestScaleBalances(t *testing.T) {
	f := newFixture(t)
	t0 := time.Now()
	f.fill()
	fillTime := time.Since(t0)
	t.Logf("набор: %d товаров, %d поступлений по %d строк, %d выдач по %d строк; проведено %d документов за %v (%v на документ); %s/%s, GOMAXPROCS=%d, %s",
		goodsN, receiptsN, receiptLines, issuesN, issueLines, f.posted, fillTime.Round(time.Millisecond), (fillTime / time.Duration(f.posted)).Round(time.Microsecond),
		runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), runtime.Version())
	t0 = time.Now()
	got := f.balances()
	t.Logf("Balances: %d строк за %v", len(got), time.Since(t0).Round(time.Microsecond))
	f.check(got)

	q, err := query.Parse(stockQuery, f.model)
	if err != nil {
		t.Fatal(err)
	}
	t0 = time.Now()
	res, err := q.Run(ctx, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Query.Run: %d строк за %v", len(res.Rows), time.Since(t0).Round(time.Microsecond))
	fromQuery := map[int64]value.Number{}
	for _, r := range res.Rows {
		fromQuery[r[0].(value.Ref).ID] = r[1].(value.Number)
	}
	f.check(fromQuery)
}

func BenchmarkPostReceipt(b *testing.B) {
	f := newFixture(b)
	for d := 0; d < 50; d++ { // прогрев и ненулевой регистр
		f.add("receipt", d, true)
	}
	ids := make([]int64, b.N)
	for i := range ids {
		ids[i] = f.add("receipt", 1000+i, false)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := f.pl.Post(ctx, "receipt", ids[i]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPostIssue(b *testing.B) {
	f := newFixture(b)
	for d := 0; d < receiptsN; d++ {
		f.add("receipt", d, true)
	}
	ids := make([]int64, b.N)
	for i := range ids {
		ids[i] = f.add("issue", i, false)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := f.pl.Post(ctx, "issue", ids[i]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBalances(b *testing.B) {
	f := newFixture(b)
	f.fill()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f.store.Balances(ctx, "stock", sqlite.BalanceQuery{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueryRun(b *testing.B) {
	f := newFixture(b)
	f.fill()
	q, err := query.Parse(stockQuery, f.model)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := q.Run(ctx, f.store, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// Повтор операций не удерживает память: после прогрева куча при тех же операциях не растёт сверх допуска.
func TestNoRetainedGrowth(t *testing.T) {
	f := newFixture(t)
	for d := 0; d < 50; d++ {
		f.add("receipt", d, true)
	}
	id := f.add("receipt", 9999, true)
	q, err := query.Parse(stockQuery, f.model)
	if err != nil {
		t.Fatal(err)
	}
	cycle := func() {
		if err := f.pl.Unpost(ctx, "receipt", id); err != nil {
			t.Fatal(err)
		}
		if err := f.pl.Post(ctx, "receipt", id); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Run(ctx, f.store, nil); err != nil {
			t.Fatal(err)
		}
	}
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	for i := 0; i < 20; i++ { // прогрев
		cycle()
	}
	before := heap()
	for i := 0; i < 300; i++ {
		cycle()
	}
	after := heap()
	growth := int64(after) - int64(before)
	t.Logf("куча после прогрева %d КиБ, после 300 циклов отмена/проведение/запрос %d КиБ (рост %d КиБ)", before/1024, after/1024, growth/1024)
	if growth > 4<<20 {
		t.Errorf("удерживаемая память выросла на %d КиБ за 300 повторов", growth/1024)
	}
}

// mallocs возвращает среднее число выделений памяти на вызов fn за n вызовов (счётчик рантайма, не зависит от скорости
// машины, поэтому бюджет по нему устойчив; время сравнивается с запасом на десятикратную разницу машин).
func mallocs(n int, fn func(i int)) float64 {
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	for i := 0; i < n; i++ {
		fn(i)
	}
	runtime.ReadMemStats(&b)
	return float64(b.Mallocs-a.Mallocs) / float64(n)
}

// Бюджеты: измерения прогона ci 40617697281 (ubuntu-latest, 4 vCPU, go1.26.0) — проведение поступления 20 строк
// 1.5 мс и 967 выделений, выдачи 10 строк 4.7 мс и 7427, Balances и Query.Run по 100 товарам (6 000 движений) 7.5 мс и
// около 75 300 выделений. Бюджет выделений — не более полутора измеренных; бюджет времени — на порядок больше измеренного.
// Узкого места, требующего оптимизации, измерения не показали (docs/implementation/performance.md).
func TestBudgets(t *testing.T) {
	f := newFixture(t)
	for d := 0; d < receiptsN; d++ {
		f.add("receipt", d, true)
	}
	const n = 30
	receipts := make([]int64, n)
	issues := make([]int64, n)
	for i := 0; i < n; i++ {
		receipts[i] = f.add("receipt", 1000+i, false)
		issues[i] = f.add("issue", i, false)
	}
	type budget struct {
		name   string
		allocs float64
		perOp  time.Duration
		run    func(i int)
	}
	q, err := query.Parse(stockQuery, f.model)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []budget{
		{"Post receipt, 20 lines", 1500, 30 * time.Millisecond, func(i int) { must(f.pl.Post(ctx, "receipt", receipts[i])) }},
		{"Post issue, 10 lines", 11000, 100 * time.Millisecond, func(i int) { must(f.pl.Post(ctx, "issue", issues[i])) }},
		{"Balances, 100 products", 113000, 150 * time.Millisecond, func(int) { _, err := f.store.Balances(ctx, "stock", sqlite.BalanceQuery{}); must(err) }},
		{"Query.Run, 100 products", 113000, 150 * time.Millisecond, func(int) { _, err := q.Run(ctx, f.store, nil); must(err) }},
	} {
		start := time.Now()
		got := mallocs(n, b.run)
		per := time.Since(start) / n
		t.Logf("%s: %.0f allocs/op (budget %.0f), %v/op (budget %v)", b.name, got, b.allocs, per.Round(time.Microsecond), b.perOp)
		if got > b.allocs {
			t.Errorf("%s: %.0f allocations per operation, budget %.0f", b.name, got, b.allocs)
		}
		if per > b.perOp {
			t.Errorf("%s: %v per operation, budget %v", b.name, per, b.perOp)
		}
	}
}
