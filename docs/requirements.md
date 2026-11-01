# Соответствие требованиям

Текущая версия предназначена для ДЗ №7. Основа предыдущих тем сохранена
в актуальной архитектуре, а временное хранение в памяти заменено PostgreSQL.

| Требование | Реализация и проверка |
| --- | --- |
| Два отдельных модуля Gateway и Ledger | `gateway/go.mod`, `ledger/go.mod`, общий `go.work`, локальный `replace` |
| GET `/ping` → 200 `pong` | `gateway/internal/api/handler.go`, unit-тест |
| `Transaction`, `Budget`, методы валидации | `ledger/models.go`, табличные unit-тесты |
| `Validatable`, `CheckValid` | `ledger/models.go`, проверка обоих типов через один интерфейс |
| Импорт JSON через `io.Reader` | `ledger/load.go`, флаг `-budgets`, `os.Open` и `bufio.NewReader` |
| Gateway импортирует Ledger | `gateway/main.go`, вызов `ledger.Open` и методов через HTTP-слой |
| DTO и преобразователи | `CreateTransactionRequest`, `TransactionResponse`, `CreateBudgetRequest`, `BudgetResponse` в `gateway/internal/api/dto.go` |
| Контракт бюджета `category`, `limit` | DTO Gateway и интеграционный HTTP-тест; SQL `limit_amount` не виден клиенту |
| Валидация до записи, Amount > 0 | В обработчиках и повторно в Ledger, тесты нулевых и отрицательных сумм |
| JSON, статусы 201/200/400/409/500, пустые массивы | `gateway/internal/api`, unit- и интеграционные HTTP-тесты |
| Middleware: метод, путь, длительность | `gateway/internal/api/middleware.go`, тест журнала |
| Схема, ограничения и миграции Goose | `ledger/migrations`, проверка up/down на отдельных схемах PostgreSQL |
| `sql.Open` + Ping, DSN из env, ошибки старта | `ledger/internal/db`, `ledger/internal/config`, `ledger/app.go` |
| UPSERT и чтение из таблиц | `ledger/service.go`, интеграционные тесты на настоящей БД |
| Бюджет: 409, запись не появляется | SQL-транзакция с блокировкой категории; отдельные HTTP- и конкурентные тесты |
| Redis, env, успешное подключение | `ledger/internal/cache`, `ledger/app.go` |
| Кеш отчёта, hit/miss, TTL 30 секунд | `GetReportSummary`, проверка TTL, кеша при закрытой БД, истечения и инвалидации |
| Данные переживают перезапуск | Повторное подключение в Go-тесте и `scripts/smoke.py` с перезапуском обоих процессов |
| Пул 10 open / 5 idle | `ledger/internal/db/postgres.go`, проверка лимита open в интеграционном тесте |
| Table-driven, t.Run, t.Parallel, покрытие | Go-тесты, `make coverage`, `make coverage-integration` |
| README двух сервисов, cURL, up/down, Redis env | `gateway/README.md`, `ledger/README.md` и корневой README |

Временные срезы и map из ДЗ №2–3 не используются: в ДЗ №7 источником данных
становится PostgreSQL. Поэтому очистка тестов выполняется через изолированные
схемы и префиксы Redis, а не через функцию `Reset` для общей базы. Демонстрационные
записи из ранних `main` перенесены в тесты и файл примера: обычный старт не меняет
пользовательские данные. CLI-программа из ДЗ №1 не нужна для работы ДЗ №7.

Отрицательные суммы не принимаются согласно валидации ДЗ №4 и №6. Ограничение
таблицы `amount <> 0` соответствует схеме ДЗ №7 и не заменяет проверку домена.
Кеш бюджетов опционален и не реализован: списки читаются из PostgreSQL.

Для сдачи требуется опубликовать изменения в GitHub и предоставить преподавателю
доступную ссылку на репозиторий. Локальная проверка сама по себе не подтверждает
публикацию или открытость доступа.
