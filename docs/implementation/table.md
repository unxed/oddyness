# Таблица: готовый goWidgets.Table (P15)

Карточка требовала проверить upstream и использовать готовый `Table`, а не заводить частный. В goWidgets
`Window.AddTable` уже есть (ревизия `5f71d2bacbd1`, закреплена в `go.mod` oddyness): заголовки столбцов с
выравниванием, `SetRows`/`Rows` (копирование на входе и выходе), `Select`/`SelectedIndex`, события `Selected`,
`Activated`, `HeaderClicked`, `SetSortIndicator`; backend без сетки получает запасной `ListBox` (`Fallback()`).
Изменений в goWidgets не потребовалось, копии в oddyness нет.

Проверка на закреплённой ревизии:
- `internal/ui/tablecheck` (headless-backend, отдельный тестовый бинарник): заголовки и выравнивание, пустой набор
  (выбор в нём невозможен), кириллица, кавычки и пустая ячейка, копирование строк, выбор программой и событием
  backend (`PickTableRow` → `Selected`), выбор вне диапазона снимает выбор, новый набор сбрасывает выбор.
- Нативный GTK: шаг `ci` запускает тест самого goWidgets `backends/gtk/tabletest` под Xvfb на закреплённой ревизии
  (`xvfb-run go test github.com/unxed/goWidgets/backends/gtk/tabletest`) и требует `--- PASS`, а не пропуск.

Не проверено: Qt, Win32, Cocoa; сортировка по заголовку (goWidgets оставляет её приложению), виртуализация и
редактор ячеек (вне границы карточки). Таблица остаётся строковой: форматирование точных чисел — забота вызывающего.
