# Монтажные ведомости ОВиК — этап 3

Обновление поверх проверенного пользователем **Stage 2**. Теперь добавлены SQL-миграция 000002, Argon2id авторизация, серверные сессии, CSRF, создание первого администратора, защищённый кабинет и минимальный реестр заказчиков. Это **не готовый продукт**: интерфейс для объектов, назначений и ведомостей ещё не разработан.

## Развёртывание поверх существующей базы (Windows PowerShell)

**Обязательно:** сохранить локальный `.env` и каталог `.git`, если он имеется. Скопировать обновлённый проект поверх прежнего, **не удаляя Docker volume**. Убедиться, что `deploy/compose.yaml` публикует `127.0.0.1:5454:5432` и `DATABASE_URL` в `.env` использует порт 5454.

```powershell
# Из корня Air_System_Ledger
Copy-Item .env .env.backup # локальная резервная копия; не коммитить

go mod tidy
go test ./...
go vet ./...

docker compose --env-file .env -f deploy/compose.yaml up -d
docker compose --env-file .env -f deploy/compose.yaml ps
```

### Миграция существующей БД (16 таблиц уже созданы из 000001)

Не выполнять `000001_core.up.sql` повторно. Перед миграцией сделайте резервную копию:

```powershell
# В переменной DATABASE_URL (и в .env) должны быть секреты — не публикуйте их.
docker compose --env-file .env -f deploy/compose.yaml exec postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc -f /tmp/pre_stage3.dump'
docker cp deploy-postgres-1:/tmp/pre_stage3.dump .\pre_stage3.dump
```

`golang-migrate` — выбранный инструмент для следующих обновлений. Если `migrate` установлен, зарегистрируйте существующую базовую миграцию как выполненную **только в той БД, где уже есть 16 таблиц**. Из PowerShell:

```powershell
# Для текущей консоли после проверки текущего .env:
$env:DATABASE_URL = (Get-Content .env | Where-Object { $_ -match '^DATABASE_URL=' } | Select-Object -First 1) -replace '^DATABASE_URL=', ''
migrate -path migrations -database $env:DATABASE_URL force 1
migrate -path migrations -database $env:DATABASE_URL up
migrate -path migrations -database $env:DATABASE_URL version
```

Ожидаемая версия: `2`. В иных БД, где уже есть таблица `schema_migrations`, **не используйте `force` без проверки состояния**. Если утилита `migrate` не установлена, для разовой проверки можно выполнить 000002 напрямую:

```powershell
Get-Content -Raw -Encoding UTF8 .\migrations\000002_auth.up.sql |
  docker compose --env-file .env -f deploy/compose.yaml exec -T postgres psql -v ON_ERROR_STOP=1 -U montage_app -d montage -1
```

При таком ручном применении история `golang-migrate` не обновится. При дальнейшей установке инструмента следует удостовериться, что миграция 000002 применена, и только потом `force 2` для синхронизации версии; **не выполнять `up` повторно**.

### Первый администратор

```powershell
go run ./cmd/admin create-admin admin "Администратор"
```

Пароль (от 12 символов) вводится интерактивно, без отображения и передачи в аргументах команды. После создания:

```powershell
go run ./cmd/server
```

Открыть `http://localhost:8080/login`, войти и открыть `/app/customers`. Под администратором доступно создание заказчика, а для других ролей — только защищённая главная страница, пока не реализованы их операции.

### Проверка SQL

```powershell
docker compose --env-file .env -f deploy/compose.yaml exec postgres psql -U montage_app -d montage -c '\dt'
```

Должно стать **17 таблиц**, в том числе `user_sessions`. Маршруты `/healthz` и `/readyz` остаются публичными. Содержимое `/app` защищено сессией; изменение данных требует CSRF-токен; CRUD заказчиков пока доступен только `admin`.

## Безопасность и ограничения

- Приложение запускается на `:8080` в HTTP для **локальной разработки**. Для внешнего доступа нужны HTTPS, корректная конфигурация защищённых cookies, ограничения сетевого доступа, rate limiting входа, отдельные права БД, аудит, резервное копирование и тест восстановления. Не публиковать сервер в Интернет в этой конфигурации.
- Пароли: Argon2id (`m=64 MiB`, `t=3`, `p=4`, 16-байтовая соль). Учетные записи с неизвестным логином также проходят дорогостоящую операцию, но rate limit всё ещё требуется.
- Сессии: токены 32 байта, в БД хранятся только SHA-256 хеши, срок жизни 12 часов, отзыв при выходе. CSRF хранится отдельно как хеш, в HTML-формах проверяется против отдельной cookie со свойством SameSite=Strict.
- В Stage 3 не реализован журнал аудита изменений: работа пока пригодна для локального пилота, не для продакшена.
- Триггер `project_revisions_status_guard` запрещает недопустимые смены статусов, а `execution_events_append_only` запрещает переписывание истории выполнения.
- Сначала прогоните миграцию на тестовом окружении. На реальной БД фактическую миграцию и интеграционные тесты может проверить только пользователь.
- В этой среде не было сетевого доступа к Go modules и PostgreSQL. Полный `go test ./...` **не выполнялся успешно**, нужно проверить на Windows после `go mod tidy`.
