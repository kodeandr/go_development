# Ledger

Ledger — отдельный Go-модуль с доменными моделями, валидацией и операциями
над PostgreSQL. Gateway импортирует этот модуль напрямую. Redis используется
только для кеша отчётов и не заменяет основную базу.

## Конфигурация

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `DATABASE_URL` | не задана | DSN, приоритетнее всех `DB_*` |
| `DB_HOST` | `localhost` | Хост PostgreSQL |
| `DB_PORT` | `5432` | Порт |
| `DB_USER` | `postgres` | Пользователь |
| `DB_PASS` | `postgres` | Пароль локальной БД |
| `DB_NAME` | `cashapp` | База данных |
| `DB_SSLMODE` | `disable` | Режим TLS для DSN из `DB_*` |
| `REDIS_ADDR` | `localhost:6379` | Адрес Redis |
| `REDIS_DB` | `0` | Номер БД Redis |
| `REDIS_PASSWORD` | пустая строка | Пароль, если требуется |
| `REDIS_KEY_PREFIX` | `cashcraft:` | Пространство ключей кеша |
| `LEDGER_ADDR` | `:8081` | Адрес диагностического HTTP-сервера |

Настройки получает процесс, в котором вызван `ledger.Open`, в том числе Gateway.
Go не читает `.env` автоматически. Реальные пароли и DSN в Git не добавляются.
Файлы `.env.example` и `compose.yaml` содержат только локальные учебные настройки.

## База и запуск

Из корня репозитория:

```bash
make up
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/cashapp?sslmode=disable'
export REDIS_ADDR=localhost:6379
export REDIS_DB=0
export REDIS_PASSWORD=''
make tools
.bin/goose -dir ./ledger/migrations postgres "$DATABASE_URL" up
make run-ledger
```

Compose создаёт БД `cashapp` при инициализации нового volume. При самостоятельной
установке PostgreSQL создайте её через
`createdb -h localhost -U postgres cashapp`. Откат одной миграции:

```bash
.bin/goose -dir ./ledger/migrations postgres "$DATABASE_URL" down
```

`ledger.Open` вызывает `sql.Open("pgx", dsn)`, `PingContext` с таймаутом,
проверяет схему и подключает Redis через официальный `go-redis/v9`.
Успешные подключения журналируются. Ошибка зависимостей или отсутствие миграций
останавливает запуск. Пул: 10 open, 5 idle, срок жизни соединения 30 минут,
максимальный простой 5 минут.

Отдельная команда `go run ./ledger/cmd/ledger` выводит `Ledger service started`
и обслуживает `GET /healthz`. Это диагностический запуск пакета; Gateway
не обращается к нему по сети, а создаёт собственный экземпляр того же Ledger.

## Бюджеты из файла

```bash
go run ./ledger/cmd/ledger -budgets ./ledger/budgets.example.json
```

Команда открывает файл через `os.Open`, оборачивает в `bufio.NewReader` и вызывает
`LoadBudgets(ctx, r io.Reader)`. Формат файла:

```json
[{"category":"еда","limit":5000.00}]
```

Весь JSON и валидация всех объектов проверяются до первой записи. Далее бюджеты
добавляются через `SetBudget`; повторный импорт обновляет существующие категории.
Без флага `-budgets` старт не создаёт демонстрационных записей.

## Операции и кеш

`SetBudget` и `AddTransaction` обязательно вызывают `Validate` до работы с БД.
`Budget.Limit` соответствует SQL-колонке `limit_amount`. Вставка расхода возвращает
сгенерированный PostgreSQL `id`. Расходы должны быть положительными, категории
без бюджета разрешены. При проверке бюджета суммируются все даты категории.

`GetReportSummary` агрегирует `SUM(amount) GROUP BY category` за период, включая
обе границы. Ключ имеет вид
`cashcraft:report:summary:<from>:<to>:<version>`, TTL — 30 секунд. После INSERT
увеличивается `cashcraft:report:version`, поэтому старые параллельные запросы
не могут заполнить актуальный ключ прежним отчётом. Старые ключи удаляются по TTL.

После запуска Gateway дважды выполните:

```bash
curl -sS 'http://localhost:8080/api/reports/summary?from=2026-01-01&to=2026-01-31'
curl -sS 'http://localhost:8080/api/reports/summary?from=2026-01-01&to=2026-01-31'
```

В журнале процесса Gateway, где выполняется Ledger, первый запрос к новому
периоду даёт `cache=miss`, повторный в пределах TTL — `cache=hit`.
При ошибке или повреждённом JSON кеша отчёт читается из PostgreSQL.

Полная проверка запросами и сохранности данных — в
[gateway/README.md](../gateway/README.md).
