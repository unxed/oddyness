# oddyness

Независимая платформа для бизнес-приложений, исследующая совместимость с
**1С:Предприятием**. Новая реализация на Go; интерфейс —
[goWidgets](https://github.com/unxed/goWidgets). Прикладные конфигурации будут
развиваться отдельно.

Название созвучно «один эс» и обыгрывает репутацию необычного языка и системы
в российской ИТ-культуре. Проект не связан с фирмой «1С».

Сейчас здесь **начальный исполняемый каркас**, а не готовая платформа и не
обещание запуска существующих конфигураций. Цель — дать сообществу точку
приложения усилий, из которой можно вырастить платформу.

Для локальной проверки из корня репозитория: `go run ./cmd/oddyness version`.
Окно формы: `go run ./cmd/oddyness form testdata/configs/small-stock /tmp/s.db Товары` (нужна графическая сессия; [forms.md](docs/implementation/forms.md)).
Хранилище: `go run ./cmd/oddyness db create testdata/configs/small-stock /tmp/s.db`, затем `catalog add|get|list` ([storage.md](docs/implementation/storage.md)).
Проверка конфигурации: `go run ./cmd/oddyness config check testdata/configs/small-stock` (формат — [config-format.md](docs/implementation/config-format.md)).
Разбор BSL: `go run ./cmd/oddyness bsl check <файл>` (подмножество — [bsl-subset.md](docs/implementation/bsl-subset.md)).
Минимальная проверка изменений: `go vet ./...` и `go test ./...`. На pull request
workflow `ci` также собирает CLI для Linux, Windows и macOS с `CGO_ENABLED=0`.

Начать работу: [план реализации](docs/implementation/plan.md).
Архитектура: [как устроена платформа](docs/implementation/architecture.md).
Процесс: [итерации и проверки](docs/implementation/workflow.md).
Сквозной пример: [«Малый склад»](docs/implementation/test-configuration.md).
Основания решений: [исследовательское досье](docs/research/README.md).
Исходные ограничения: [задание и план](docs/research/00-brief.md).

Лицензия собственного кода и материалов проекта: [BSD-3-Clause](LICENSE).
Ссылки на чужие проекты не меняют их лицензий и не означают включения их кода.
