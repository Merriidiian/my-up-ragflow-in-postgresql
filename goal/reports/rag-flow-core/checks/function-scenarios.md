# Функциональный аудит: 21.10.2026

[352 маршрута OSS](function-routes.json): для каждого указаны необходимая положительная семантика, существенные отрицательные сценарии, зависимости, причина непроверенности и отдельный унаследованный результат. **77** сопоставлений с прежней HTTP-матрицей остаются историческими. Новой live HTTP-приёмки всех маршрутов нет. Регистрация handler и общий unit-success не равны выполнению маршрута на новом стеке.

| Сценарий | Унаследованное свидетельство | Новая проверка / ограничение |
|---|---|---|
| Новая PG18.6, все 53 модели | pg3 validation | Новый disposable PG: схема, CRUD каждой модели, JSON/NULL/уникальность, русские строки; точные результаты в итоговом go-summary |
| pg1/pg2 → pg3, повтор, drift | pg3 validation | Новые TestPostgresUpgradeAndDrift / DuplicateRollback / MetadataSchema; рабочая pg3 не менялась |
| Исторический marker vs PG-каталог | Статически присутствует в upstream | Новый отдельный тест независимости маркеров; неизвестная PG-версия не «чинится» legacy marker |
| Импорт MySQL Go RC2 → пустая PG | Прежний реальный fixture import | Новый настоящий disposable MySQL8.4.6/PG18.6: 53 таблицы, ID/history/citations, hashes/sequence, отказ populated target, rollback duplicates |
| Настройка metadata backend | В pg3 ещё был активен MySQL runtime | Теперь PostgreSQL default; env/YAML/DAO отклоняют MySQL/OceanBase; положительный и отрицательные config-тесты |
| Датасеты/документы/пагинация | Прежние HTTP CRUD + TXT/Office | Новый Go fixture/unit слой; новая HTTP/ingestion проверка на общем стеке pending |
| TXT/DOCX/XLSX/text-PDF | Реальные прежние ingestion→chunks→retrieval | Новые parser/ingestion unit-тесты; прежние реальные форматы не объявляются повторёнными |
| Русский scan PDF | **Failed**, потерян «Нева-31» | Новый аудит runtime-словаря доказал непредставимость факта. [OCR](ocr-investigation.md); scan→chunks→retrieval→citation pending совместимых весов |
| Штатный pipeline c PG + S3 | upstream integration fixture использовал MySQL | Тест переведён на явно заданную disposable PG и собственную schema; только compile-check, фактический запуск с S3 pending |
| RAG/chat/history/citations | Прежняя TXT-цепочка, JSON/SSE/скачивание/чужой доступ | Новые service/handler/DAO unit-проверки; новая генерация и реальные источники pending полного отдельного стека |
| Reasoning/SSE, EOF/error/cancel | Прежние HTTP/SSE; durable cancel не доказан | Новый unit слой сохраняется отдельно; закрытие HTTP не равно остановке GPU |
| Queued cancel/recovery/reindex | Прежний disposable worker kill/restart, 35 чанков без дублей | Новый restart/fault injection запрещён в текущей фазе; прежнее свидетельство сохранено |
| Агенты/skills/groups/memory/templates | Часть прежней HTTP-матрицы | Новые component/handler/DAO unit-тесты; каждый непроверенный live маршрут остаётся pending |
| ACL / две identity / отзыв / подмена ID | Прежний ограниченный foreign-source тест | Новый fixture слой, без заявления об OIDC. Нужны реальные identity и доказанное mapping Keycloak→RAGFlow |
| Ошибки RAGFlow/LLM, права на sources | Есть unit и ограниченные прежние live кейсы | Новый unit слой не заменяет end-to-end отказ зависимостей и отзыв прав |
| Upload ровно 200 МиБ/+1 | Прежняя boundary проверка, не индексация | Константа 209715200 сохранена; нового большого upload/ingestion нет |
| ASR/TTS/VLM/rerank | Нет подготовленных локальных моделей | Unit/mock допустим; живые способности не подтверждены, модели не скачиваются |
| Внешние OAuth/SMTP/cloud/connectors/web search/MCP | Не включались | Функции не удалены; внешнее исполнение запрещено текущим контуром. MCP по умолчанию выключен |
| EE | Не OSS | Не приписывается OSS-покрытию |
| Сетевая изоляция runtime | Прежний isolation.json | Проверочные контейнеры без внешней сети; это не новый packet capture полной пользовательской нагрузки |
| Совместный PG/S3/Infinity/Kvrocks/NATS restore | Не доказан | Зависимость infrastructure: согласованная точка backup/restore и отдельный disposable стек |

Новый итог: 15405 успешных тестов/подтестов, 64 skipped, 0 ошибок; vet и три сборки прошли. Реальный бинарник дважды выполнил PG `--migrate` с сохранением seed-хеша: [CLI migration](cli-migration.json). Runtime DeepDoc зарегистрирован: [smoke](runtime-smoke.json). Полный Go-прогон и исходные failures/ремонт окружения документируются отдельно; счётчик включает подтесты и не является числом проверенных пользовательских функций. Окончательный машинный итог — [go-summary.json](go-summary.json); исходные логи — исходный workspace (сырые журналы не публикуются).
