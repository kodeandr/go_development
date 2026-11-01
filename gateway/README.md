# Gateway

HTTP-слой проекта: Gateway импортирует модуль Ledger из соседнего каталога
через локальный `replace` в `go.mod`. DTO и преобразования находятся в
`internal/api/dto.go`; SQL и бизнес-правила — в Ledger. Межпроцессного HTTP-
или gRPC-вызова на данном этапе нет.

## Запуск

Сначала из корня репозитория запустите PostgreSQL, Redis, примените миграции
и проверьте отдельную команду Ledger:

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

В другом терминале экспортируйте те же настройки, затем:

```bash
set -a; . ./.env; set +a
make run-gateway
```

`GATEWAY_ADDR` по умолчанию `:8080`. Можно запускать отдельно:
`cd gateway && go run .`. Gateway создаёт экземпляр `ledger.Service` при старте,
поэтому настройки подключения нужны и ему. Подробности — в
[инструкции Ledger](../ledger/README.md).

## cURL

Проверка сервера из ДЗ №2:

```bash
curl -i http://localhost:8080/ping
# 200, pong
```

Создание бюджета (`201`). Повторный POST обновляет лимит без второй строки:

```bash
curl -i -X POST http://localhost:8080/api/budgets \
  -H 'Content-Type: application/json' \
  -d '{"category":"Продукты","limit":5000.00}'
```

Расход в пределах лимита (`201`, в ответе есть сгенерированный `id`):

```bash
curl -i -X POST http://localhost:8080/api/transactions \
  -H 'Content-Type: application/json' \
  -d '{"amount":1250.50,"category":"Продукты","description":"Покупка в магазине","date":"2026-01-15"}'
```

Превышение бюджета (`409 {"error":"budget exceeded"}`, вставки нет):

```bash
curl -i -X POST http://localhost:8080/api/transactions \
  -H 'Content-Type: application/json' \
  -d '{"amount":4000.00,"category":"Продукты","description":"Сверх лимита","date":"2026-01-15"}'
```

Списки из PostgreSQL (`200`, пустой список — `[]`):

```bash
curl -sS http://localhost:8080/api/budgets
curl -sS http://localhost:8080/api/transactions
```

Отчёт, оба параметра обязательны, обе даты включены:

```bash
curl -sS 'http://localhost:8080/api/reports/summary?from=2026-01-01&to=2026-01-31'
curl -sS 'http://localhost:8080/api/reports/summary?from=2026-01-01&to=2026-01-31'
```

На чистой базе после примеров выше:

```json
{
  "from": "2026-01-01",
  "to": "2026-01-31",
  "total": 1250.50,
  "by_category": [{"category": "Продукты", "amount": 1250.50}]
}
```

В консоли Gateway видны `cache=miss`, затем `cache=hit`. TTL — 30 секунд.
Middleware пишет HTTP-метод, путь и длительность каждого запроса.

Все ответы API имеют `Content-Type: application/json; charset=utf-8`.
Единый формат ошибки — `{"error":"..."}`: 400 для невалидных данных,
409 для бюджета, 500 для неожиданных ошибок; также предусмотрены JSON 404/405
и 415 для неверного Content-Type. `/ping` возвращает текст `pong`, как в ДЗ №2.

Поле бюджета — именно `limit`. Сумма расхода строго положительная. Деньги
передаются числами с максимум двумя дробными знаками, например `1250.50`.
Клиент не задаёт `id` расхода: он присваивается базой.

## Перезапуск

Остановите и снова запустите отдельную команду Ledger; повторите GET через
Gateway. Затем перезапустите сам Gateway: он заново создаст свой экземпляр
Ledger и прочитает прежние данные из PostgreSQL. Полностью этот сценарий
автоматизирует `make smoke`; состояние процесса не является хранилищем.

Откат выполняется отдельно, при остановленных приложениях:

```bash
.bin/goose -dir ./ledger/migrations postgres "$DATABASE_URL" down
```
