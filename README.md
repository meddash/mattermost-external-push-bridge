# Mattermost External Push Bridge

Server-only Mattermost plugin that listens to `NotificationWillBePushed`, preserves the standard Mattermost push flow, and asynchronously forwards one logical event per `server_id + post_id + recipient_user_id + notification_type` to an external HTTP API.

## Target Mattermost Version

The repository was empty, so the plugin was created from scratch and targeted to the current public Mattermost plugin API module:

- `github.com/mattermost/mattermost/server/public` `v0.4.3`
- `NotificationWillBePushed` minimum server version: `9.0`
- manifest `min_server_version`: `9.0.0`

## Hook Research

Research was verified against Mattermost public/plugin API docs and current upstream server sources:

- `NotificationWillBePushed` is defined in the plugin `Hooks` interface and is called before a push notification is sent to the push notification server.
- In `sendPushNotificationToAllSessions`, the hook is executed once per target user before Mattermost fetches mobile sessions and before the per-session/device loop starts.
- Because the hook runs before `getMobileAppSessions`, it is invoked even when the user later turns out to have zero eligible mobile sessions.
- The hook is part of the standard push pipeline, so it runs only when Mattermost has already decided to build a push notification for that user.
- The standard Mattermost push is not blocked or modified by this plugin. The hook always returns `nil, ""`.

Observed relevant source behavior:

- Hook signature: `NotificationWillBePushed(pushNotification *model.PushNotification, userID string) (*model.PushNotification, string)`
- Hook minimum server version: `9.0`
- `SendPushNotification` documentation explicitly says the hook also runs for push notifications created by plugins.
- The hook runs before the server fetches the user mobile sessions and before iterating over device sessions.

### What This Means Practically

- Invocation granularity: one hook call per target user, not one call per registered device.
- Multi-device users can still cause multiple actual Mattermost device sends, but this plugin deduplicates them into one logical external event.
- If Mattermost never enters its push-notification path for a given situation, this plugin will not see it and intentionally does not reimplement recipient calculation.

### Important Limitation

This plugin depends on Mattermost entering the push notification path. If your separate mobile application is not registered as a Mattermost mobile session / push device, the hook does not create a new notification universe by itself; it only mirrors Mattermost’s own push decision.

The current upstream call path shows the hook is executed before session lookup, but still from the standard push subsystem. If Mattermost is configured in a way that prevents push notifications from being produced at all, this plugin will not receive those events. This is a product/core limitation, not a plugin-side recipient-logic bug.

### `PushNotification.Type` and `PushNotification.SubType`

From Mattermost `model.PushNotification`:

- `Type` values exposed by current model constants include: `message`, `clear`, `update_badge`, `session`, `test`
- `SubType` is extensible; current upstream constant exposed in the model is `calls`
- This plugin processes only `Type == "message"` and ignores `SubType == "calls"`

## Architecture

1. `NotificationWillBePushed`
2. Fast config check
3. Early recipient filtering via `TestUsernames`
4. Lightweight enrichment for recipient, post, sender, channel, team
5. Deterministic `event_id`
6. Durable KV outbox insert with `pending` state
7. Non-blocking enqueue into in-memory worker queue
8. Worker goroutines perform HTTP POST with retries and idempotency header

Core properties:

- standard Mattermost push remains untouched
- one logical external event per user/post/type
- durable restart recovery through plugin KV store
- retry with exponential backoff and jitter
- `Retry-After` support
- health endpoint and metrics endpoint
- no message text in logs

## Configuration

System Console settings:

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

`AdditionalHeaders` format:

```json
{"X-Env":"test","X-Source":"mattermost"}
```

`TestUsernames`:

Список логинов Mattermost через запятую, для которых плагин будет отправлять события во внешний API. Если поле не заполнено, плагин работает для всех пользователей.

## Payload Schema

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
    "message": "optional, omitted when IncludeMessageText=false",
    "has_files": false,
    "file_ids": []
  }
}
```

## Build And Test

```bash
make test
make bundle
```

Bundle output:

```text
dist/com.company.external-push-bridge-0.1.0.tar.gz
```

## Installation

1. Build the bundle with `make bundle`
2. Open Mattermost System Console
3. Go to `Plugins > Plugin Management`
4. Choose `Upload Plugin`
5. Upload `dist/com.company.external-push-bridge-0.1.0.tar.gz`
6. Configure the plugin in System Console and enable it

## Example Configuration

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

## Health And Metrics

- Health: `/plugins/com.company.external-push-bridge/health`
- Metrics: `/plugins/com.company.external-push-bridge/metrics`

## Manual Mock Receiver

Simple local receiver:

```bash
python3 -m http.server 8081
```

Better JSON inspection:

```bash
nc -lk 8081
```

Or run any HTTP echo service and set `ExternalAPIURL` to it.

## Manual Integration Checklist

- DM notification
- channel mention
- followed thread reply
- muted channel
- channel notification setting `all`
- one recipient with multiple Mattermost mobile devices
- recipient with no Mattermost mobile device
- standard Mattermost push service disabled or unavailable
- external API temporarily returning `500`

## Remaining Production Risks

- Whether your exact Mattermost deployment still enters the standard push path when the built-in push service is disabled must be verified in that environment.
- Queue overflow still falls back to durable outbox only; real-time forwarding is deferred until recovery/restart or next manual requeue event.
- Current normalized notification reason is conservative and only fills reliable cases.
- HA dedup is based on deterministic `Idempotency-Key` plus KV CAS on each node; exactly-once semantics still depend on the external API honoring idempotency.
