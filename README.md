# RAGFlow RC2 с PostgreSQL

Продолжение RAGFlow **v1.0.0-rc2**, исходная точка — PostgreSQL **18.6 / pg3**. PG-драйвер, SQL-совместимость, advisory locks, каталог схемы и миграции находятся в `internal/`. Применение внешнего patchset и чтение второй копии исходников для сборки не нужны. Исходники импорта — `cmd/pg_migrate` и `internal/dao/postgres_import*`.

Происхождение: upstream archive SHA256 `0e523d52190be29f1bc4df4640efc1f2d183880555e8644eac269406a1a12678`; сохранён изменённый pg3, сверенный координатором с patch SHA256 `fe15387ef1db1fd2c25a2deb8ba3806a8a67d8bb4991af8893445921b4aea87a`. Upstream [LICENSE](LICENSE) сохранена. Собственная лицензия проекта этим переносом не назначается. Точный пофайловый срез и новые результаты — [отчёт core](goal/reports/rag-flow-core/status.md).

Оставшиеся проблемы и критерии завершения: [MIGRATION_STATUS.md](MIGRATION_STATUS.md).

## Офлайн-сборка

Нужен подготовленный Linux ARM64 builder `infiniflow/github_action_runner@sha256:07712b55ee67788c147f5f3974cc5628270e99ce85edfe30c526c21a27a3371e`, Go 1.27.1, локальный module cache, PCRE2/pkgconf из `native-packages.lock.json`, CMake/C++ и нативные office_oxide 0.1.12, pdfium 7809, pdf_oxide 0.3.73, ONNX Runtime 1.29.0. Таблица cl100k_base должна быть подготовлена в `ragflow_deps/` или `/opt/ragflow_deps/`. Библиотеки — в `~/ragflow-native-libs/` или `/opt/ragflow-native-libs/` builder. Веса не входят в исходное дерево.

`scripts/offline-go.sh` использует штатную сборку C++/CGO и запрещает downloader-вызовы; отсутствующий комплект вызывает ошибку. Запускать в builder без внешней сети, с `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, собственным `GOCACHE` и подготовленным read-only `GOMODCACHE`. Модули не скачиваются.

```sh
scripts/offline-go.sh test -tags cgo,static -json -count=1 -timeout 180s ./...
scripts/offline-go.sh vet -tags cgo,static ./...
mkdir -p bin
scripts/offline-go.sh build -tags cgo,static -trimpath -o bin/ragflow_server cmd/ragflow_server.go cmd/deepdoc_server_ee.go
scripts/offline-go.sh build -tags cgo,static -trimpath -o bin/ragflow-cli cmd/ragflow-cli.go
scripts/offline-go.sh build -tags cgo,static -trimpath -o bin/ragflow_pg_migrate ./cmd/pg_migrate
docker build --network none --pull=false -t vsm/ragflow:core-review .
```

Dockerfile сохраняет ресурсы закреплённого runtime RC2 и запускает бинарник напрямую. Старый upstream entrypoint с MySQL-процедурами не используется. Конфигурация, секреты и lifecycle общего стека принадлежат инфраструктуре. Действующий стенд этим компонентом не переключается.

## PostgreSQL и миграции

Метаданные runtime допускают только PostgreSQL, по умолчанию `postgres`; MySQL/OceanBase отвергаются на уровнях конфигурации и DAO. MySQL-клиент метаданных сохранён в CLI импорта и его тестах. Штатные коннекторы/SQL-tools RAGFlow сохранены отдельно; это не второй metadata backend и не разрешение внешнего доступа.

Используются приватный service config и `RAGFLOW_POSTGRES_{HOST,PORT,NAME,USER,PASSWORD,SSLMODE}`. В исходниках нет рабочего пароля. `sslmode` по умолчанию `verify-full`; `disable` допустим только в изолированном локальном профиле. Шаблон `conf/service_conf.yaml` не является рабочей конфигурацией стенда. Infinity 0.7.4, S3 `minio`/pgsty/silo, Kvrocks 2.16.0, NATS JetStream 2.14.2 сохраняются. Лимит файла — 209715200 байт (200 МиБ).

После backup и в разрешённом окне обслуживания выполняют один процесс:

```sh
bin/ragflow_server --migrate --config /run/private/service_conf.yaml
```

Новая схема создаётся транзакционно. `pg1 → pg2 → pg3` идёт под отдельной advisory lock; повторный запуск pg3 проверяет встроенный каталог. Структура pg3 не менялась. Неизвестная версия, дрейф, дубликаты и схема без PG-маркера отклоняются; автоочистки или сброса нет. API/ingestor проверяют готовую схему, не выполняют старую MySQL-цепочку.

`mysql_migration.database.version` — исторический маркер миграций данных upstream. Он может сохраняться при импорте и читается дополнительной защитой от downgrade версии приложения. Он **не заменяет** `vsm_pg_schema` и встроенный PG-каталог, не разрешает неизвестную PG-схему и не запускает MySQL-миграции. Само `DB_TYPE=postgres` не является доказательством корректности переноса существующих данных.

## Импорт и откат

Импортёр переносит только совместимую Go-схему RC2 MySQL в пустую PG. Старую Python-схему/произвольный dump сначала приводят к нужному upstream формату на копии. Перед импортом останавливают запись источника и цели в отдельно согласованное окно; S3/Infinity/Kvrocks/NATS сохраняют отдельно в согласованной точке. DSN задают приватно через `RAGFLOW_IMPORT_MYSQL_DSN` и `RAGFLOW_IMPORT_POSTGRES_DSN`, затем:

```sh
RAGFLOW_IMPORT_OFFLINE=yes bin/ragflow_pg_migrate
```

Источник читается в read-only repeatable-read snapshot. Цель блокируется; ID, JSON, NULL, история/цитаты и sequences сохраняются; числа строк и SHA256 проверяются. Непустая цель/несовместимая схема/некорректные значения вызывают отказ. Ошибка импорта откатывает целевую транзакцию. Рабочая MySQL-база пользователя этим прогоном не проверялась.

Откат не выполняют снижением PG-маркера. Неуспешная транзакционная миграция откатывается сама; после успешной миграции возврат — проверенный backup вместе с соответствующим бинарником и согласованными данными остальных хранилищ. Не запускать старый бинарник против более новой неизвестной схемы.

## Изолированная регрессия и OCR

`scripts/test_postgres_isolated.py --help` описывает одноразовые PG18.6/MySQL fixtures. Требует заранее подготовленные образы/модули/native-debs и свой scratch; создаёт internal network без портов, tmpfs для БД, удаляет только собственные контейнеры/сеть. Не читает реквизиты действующего стенда. Отчёты направлять исключительно в `goal/reports/rag-flow-core/`. Это доменный тест схемы/импорта, не запуск второго полного стека.

Русский OCR остаётся незавершённым: словарь текущего CTC-распознавателя не содержит букв контрольного факта. `scripts/check_ocr_vocabulary.py /prepared/ocr.res --text Нева-31` проверяет необходимое условие покрытия алфавита; это не проверка качества распознавания. Менять словарь отдельно от весов нельзя. Совместимые веса/runtime требуют подготовленного офлайн-комплекта, проверки лицензии и отдельной интеграционной проверки скан → чанки → retrieval → цитата. Переключения runtime/загрузки моделей здесь нет.
