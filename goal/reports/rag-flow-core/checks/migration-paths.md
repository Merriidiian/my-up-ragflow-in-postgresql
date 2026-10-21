# Пути миграции: аудит 21.10.2026

Наличие `DB_TYPE=postgres` — только выбор конфигурации. Следующие пути проверяются раздельно.

| Путь | Код/маркер | Проверка |
|---|---|---|
| Новая PostgreSQL | `dao.InitDB → initPostgresDB → runPostgresMigrations`; `vsm_pg_schema`, embedded `postgres_schema_v3.json` | `TestPostgresMetadataSchema`, `TestPostgresEveryMetadataModelRoundtrip` |
| pg1/pg2 → pg3 и повтор pg3 | Та же PG-ветвь, advisory lock, транзакция, проверка предыдущего каталога; без `RunMigrations` | `TestPostgresUpgradeAndDrift`, `TestPostgresUpgradeDuplicateRollback` |
| Запуск API/ingestor | `InitDB(migrate=false) → verifyPostgresSchema`; затем отдельный application-release downgrade guard | `TestPostgresLegacyMigrationMarkerIsIndependent` + тесты каталога |
| Исторический `mysql_migration.database.version` | `system_settings`: совместимый маркер upstream-миграций данных, читается `GetDatabaseMigrationVersion` через SQL-normalizer; не PG schema-version | Проверка отсутствующего/сохранённого маркера, отказ неизвестной PG-версии, несмотря на legacy marker |
| MySQL Go RC2 → пустая PG | `cmd/pg_migrate → PreparePostgresMetadata → ImportMySQLMetadata`; read-only источник, target transaction/hashes/sequences | `TestMySQLToPostgresMetadata` |
| Старый Python/MySQL dump | Не поддержан напрямую: необходима штатная upstream-миграция копии к совместимой Go RC2 | Нового живого подтверждения нет; не расширять заявление об импорте |

MySQL/OceanBase как metadata runtime теперь отклоняются конфигурацией, env и DAO; старые специфичные MySQL dialector/session-lock helpers перенесены в `_test.go`, их регрессии сохранены. Документные коннекторы/SQL-tools не удалены.

Dockerfile вызывает бинарник напрямую, по умолчанию `--help`; не наследует upstream MySQL entrypoint. Поставщик инфраструктуры должен явно выполнить PG migration mode до API/ingestor; эти команды не запускались в действующем стенде. Нельзя объявлять обновление реальной pg3 выполненным по disposable schema-тестам.
