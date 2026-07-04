# Mattermost External Push Bridge

Server-only плагин для Mattermost, который перехватывает `NotificationWillBePushed`, не вмешивается в стандартную отправку push-уведомлений Mattermost и асинхронно передаёт одно логическое событие во внешний HTTP API.

## Целевая версия Mattermost

- модуль: `github.com/mattermost/mattermost/server/public v0.4.3`
- минимальная версия сервера для `NotificationWillBePushed`: `9.0`
- `min_server_version` в `plugin.json`: `9.0.0`

## Что делает плагин

Плагин:

- использует `NotificationWillBePushed`
- обрабатывает только `Type == "message"`
- не изменяет и не отменяет штатный push Mattermost
- создаёт детерминированный `event_id`
- выполняет дедупликацию по `server_id + post_id + recipient_user_id + notification_type`
- сохраняет событие в durable KV outbox
- ставит событие в неблокирующую in-memory очередь
- отправляет HTTP POST отдельными worker goroutine
- повторяет временные ошибки с exponential backoff и jitter
- поддерживает `Retry-After`
- восстанавливает `pending`/`processing` события после перезапуска

## Исследование hook

По текущему Mattermost API и upstream source:

- `NotificationWillBePushed` вызывается один раз на пользователя, а не один раз на устройство
- hook вызывается до цикла по мобильным сессиям пользователя
- hook вызывается до фактической отправки в Mattermost push service
- Mattermost к этому моменту уже решил, что конкретному `userID` нужно сформировать push
- плагин получает именно `userID` получателя, а не всех участников канала

Практический вывод:

- если у пользователя несколько устройств Mattermost, внешний API всё равно получит одно логическое событие
- плагин не пересчитывает notification logic Mattermost самостоятельно
- если Mattermost не вошёл в свой push pipeline, плагин событие не увидит

## Важное ограничение

Плагин отражает только решение стандартного push-пайплайна Mattermost. Он не создаёт собственную альтернативную систему вычисления получателей. Поэтому поведение вашей инсталляции при отключённом штатном Mattermost push service или при отсутствии Mattermost mobile session / device registration нужно проверять интеграционно.

## Поддерживаемые поля `PushNotification`

Из текущей модели Mattermost:

- `Type`: `message`, `clear`, `update_badge`, `session`, `test`
- `SubType`: расширяемое поле; в актуальной модели явно есть `calls`

Плагин обрабатывает только:

- `Type == "message"`

Плагин игнорирует:

- `SubType == "calls"`
- события без `post_id`
- системные и непочтовые push-события
- собственные сообщения пользователя

## Архитектура

1. Mattermost вызывает `NotificationWillBePushed`
2. Плагин быстро читает runtime-конфиг
3. Рано применяет фильтр `TestUsernames`
4. Получает `recipient`, `post`, `sender`, `channel`, `team`
5. Формирует payload
6. Вычисляет `event_id`
7. Пытается атомарно записать событие в KV outbox со статусом `pending`
8. Кладёт `event_id` в in-memory очередь
9. Worker выполняет HTTP POST
10. При временной ошибке событие переводится обратно в `pending` и переотправляется
11. При успехе статус становится `delivered`
12. При исчерпании попыток статус становится `failed`

## Конфигурация

Поддерживаемые настройки:

- `Enabled`
- `ExternalAPIURL`
- `AuthorizationType`
- `AuthorizationToken`
- `IncludeMessageText`
- `MaxMessageTextLength`
- `RequestTimeoutSeconds`
- `MaxRetries`
- `InitialRetryDelayMilliseconds`
- `MaxRetryDelaySeconds`
- `WorkerCount`
- `QueueSize`
- `TLSVerify`
- `AdditionalHeaders`
- `TestUsernames`

### `TestUsernames`

Список логинов Mattermost через запятую, для которых плагин будет отправлять события во внешний API. Если поле не заполнено, плагин работает для всех пользователей.

Пример:

```text
ivanov, petrov, sidorova
```

Логика:

- пустая строка → плагин работает для всех
- заполненная строка → плагин работает только для перечисленных получателей
- сравнение идёт по `recipient.username`
- пробелы удаляются
- пустые значения игнорируются
- дубликаты удаляются
- регистр нормализуется

## Формат payload

```json
{
  "event_id": "sha256",
  "event_type": "mattermost_message_notification",
  "created_at": "2026-07-04T10:15:30.123Z",
  "mattermost": {
    "server_id": "server-id",
    "notification_type": "message",
    "notification_subtype": "",
    "raw_notification_type": "message",
    "raw_notification_subtype": "",
    "notification_reason": "direct_message"
  },
  "recipient": {
    "user_id": "recipient-user-id",
    "username": "recipient",
    "display_name": "Recipient Name"
  },
  "sender": {
    "user_id": "sender-user-id",
    "username": "sender",
    "display_name": "Sender Name",
    "is_bot": false
  },
  "channel": {
    "channel_id": "channel-id",
    "channel_type": "O",
    "name": "town-square",
    "display_name": "Town Square",
    "team_id": "team-id",
    "team_name": "team-name"
  },
  "post": {
    "post_id": "post-id",
    "root_id": "",
    "is_thread_reply": false,
    "create_at": 1783150530123,
    "create_at_iso": "2026-07-04T10:15:30.123Z",
    "post_type": "",
    "message": "необязательное поле",
    "has_files": false,
    "file_ids": []
  }
}
```

Если `IncludeMessageText=false`, поле `post.message` полностью отсутствует в JSON.

## Сборка и тесты

```bash
make test
make bundle
```

Артефакт плагина:

```text
dist/com.company.external-push-bridge-0.1.0.tar.gz
```

## Установка

1. Выполнить `make bundle`
2. Открыть Mattermost System Console
3. Перейти в `Plugins > Plugin Management`
4. Нажать `Upload Plugin`
5. Загрузить `dist/com.company.external-push-bridge-0.1.0.tar.gz`
6. Открыть настройки плагина и заполнить конфигурацию
7. Включить плагин

## Пример настроек

```text
Enabled = true
ExternalAPIURL = https://example.internal/api/push-events
AuthorizationType = bearer
AuthorizationToken = <secret>
IncludeMessageText = false
MaxMessageTextLength = 200
RequestTimeoutSeconds = 5
MaxRetries = 5
InitialRetryDelayMilliseconds = 500
MaxRetryDelaySeconds = 30
WorkerCount = 4
QueueSize = 2048
TLSVerify = true
AdditionalHeaders = {"X-Environment":"staging"}
TestUsernames = ivanov, petrov
```

## Диагностика

- health endpoint: `/plugins/com.company.external-push-bridge/health`
- metrics endpoint: `/plugins/com.company.external-push-bridge/metrics`

## Ручная интеграционная проверка

Рекомендуется проверить:

- DM
- mention в канале
- reply в followed thread
- muted channel
- channel notification setting `all`
- несколько устройств одного пользователя
- пользователя без Mattermost mobile device
- отключённый стандартный push service
- временно недоступный внешний API

## Mock endpoint для тестирования

Можно использовать любой HTTP echo/mock server.

Пример:

```bash
nc -lk 8081
```

или отдельный HTTP mock, принимающий POST JSON.

## Остаточные production-риски

- Точное поведение при отключённом штатном Mattermost push service нужно подтверждать в вашей инсталляции интеграционно.
- Exact-once доставка между HA-нодами зависит не только от KV CAS, но и от того, как внешний API обрабатывает `Idempotency-Key`.
- При переполнении in-memory очереди событие остаётся в durable outbox, но не уходит мгновенно до следующего recovery/requeue цикла.
- Нормализованная причина уведомления заполняется только в надёжно определяемых случаях.
