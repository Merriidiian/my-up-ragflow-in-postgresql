# Проверенный срез PostgreSQL-порта

21.10.2026. В `main` репозитория `Merriidiian/my-up-ragflow-in-postgresql` экспортированы текущие исходники PG18.6/pg3 и очищенные результаты компонентных проверок. Содержимое `rag-flow-core/` первоначального workspace размещено в корне этого репозитория. Нового полного прогона во время самой Git-публикации не выполнялось: проверялась идентичность экспортированных исходников проверенному дереву.

**15405 успешных тестов/подтестов, 64 skipped, 129 успешных пакетов, 0 ошибок**; vet и три CGO/static-сборки прошли. Реальные disposable PG18.6/MySQL8.4.6, все 53 модели, импорт, повторные CLI-миграции и runtime DeepDoc smoke подтверждены в приложенных сводках. Это не полная live-приёмка всех функций.

Основной документ: [MIGRATION_STATUS.md](../../../MIGRATION_STATUS.md). Сборка и миграции: [README](../../../README.md).

- [Go summary и skipped](checks/go-summary.json), [поимённые результаты](checks/test-results.json), [сборки и SHA256](checks/isolated-run.json).
- [Импорт 53 таблиц](checks/mysql-import.json), [двойной CLI migrate](checks/cli-migration.json), [runtime smoke](checks/runtime-smoke.json).
- [Русский OCR](checks/ocr-investigation.md), [352 маршрута](checks/function-routes.json), [сценарии](checks/function-scenarios.md), [пути миграций](checks/migration-paths.md).
- [Происхождение](checks/source-copy.json), [контроль исходного переноса](checks/final-source-audit.json), [исторические свидетельства](checks/inherited-evidence.json).

Сырые журналы, исходные исторические материалы и закрытое окружение не публикуются. Их относительные пути в исторических манифестах обозначают первоначальный workspace. Действующий стенд/его данные не менялись. Полная готовность не заявлена: OCR, новый живой аудит/ACL, интеграция рабочей pg3 и согласованный restore ещё требуют работы.
