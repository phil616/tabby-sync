# Tabby configuration sync protocol research

Research date: 2026-06-20

This document records the compatibility contract used by this project. The
Tabby client source is treated as authoritative; the original `tabby-web`
implementation is used to determine response shapes and persistence semantics.

## Sources reviewed

- Tabby client:
  [`tabby-settings/src/services/configSync.service.ts`](https://github.com/Eugeny/tabby/blob/6a697edc98feba0323fc3ae45f3f1f64d339c5c3/tabby-settings/src/services/configSync.service.ts)
- Tabby sync settings UI:
  [`configSyncSettingsTab.component.ts`](https://github.com/Eugeny/tabby/blob/6a697edc98feba0323fc3ae45f3f1f64d339c5c3/tabby-settings/src/components/configSyncSettingsTab.component.ts)
- Original server routes:
  [`backend/tabby/app/api/__init__.py`](https://github.com/Eugeny/tabby-web/blob/779867c480f9d68f32c00fd8e23d8a6f63752a34/backend/tabby/app/api/__init__.py)
- Original config API:
  [`backend/tabby/app/api/config.py`](https://github.com/Eugeny/tabby-web/blob/779867c480f9d68f32c00fd8e23d8a6f63752a34/backend/tabby/app/api/config.py)
- Original data model:
  [`backend/tabby/app/models.py`](https://github.com/Eugeny/tabby-web/blob/779867c480f9d68f32c00fd8e23d8a6f63752a34/backend/tabby/app/models.py)
- Original bearer-token middleware:
  [`backend/tabby/middleware.py`](https://github.com/Eugeny/tabby-web/blob/779867c480f9d68f32c00fd8e23d8a6f63752a34/backend/tabby/middleware.py)
- HTTPS enforcement and threat analysis:
  [Tabby PR #11228](https://github.com/Eugeny/tabby/pull/11228)
- Service retirement context:
  [Tabby issue #9131](https://github.com/Eugeny/tabby/issues/9131)
- Local authentication limitations:
  [tabby-web issue #116](https://github.com/Eugeny/tabby-web/issues/116)
- Independent compatibility reference:
  [`Clem-Fern/rtabby-web-api`](https://github.com/Clem-Fern/rtabby-web-api)

The inspected upstream revisions were:

- Tabby: `6a697edc98feba0323fc3ae45f3f1f64d339c5c3`
- tabby-web: `779867c480f9d68f32c00fd8e23d8a6f63752a34`
- rtabby-web-api: `a2d034de631107bababb25261a7c855056eacdd4`

## Client request contract

The desktop client removes one trailing slash from the configured host and
then appends one of these paths:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/1/user` | Validate the host and token |
| `GET` | `/api/1/configs` | List the authenticated user's configs |
| `POST` | `/api/1/configs` | Create a config |
| `GET` | `/api/1/configs/{id}` | Download one config |
| `PATCH` | `/api/1/configs/{id}` | Upload config content and client version |
| `DELETE` | `/api/1/configs/{id}` | Delete one config |

All requests use:

```http
Authorization: Bearer <secret sync token>
```

The client does not use cookies, OAuth, the Tabby web frontend, application
version distribution, or the connection gateway for configuration sync.

## Data contract

A config object has this effective shape:

```json
{
  "id": 1,
  "name": "Workstation",
  "content": "version: 7\nprofiles: []\n",
  "last_used_with_version": "1.0.223",
  "created_at": "2026-06-20T10:00:00.000Z",
  "modified_at": "2026-06-20T10:01:00.000Z"
}
```

Important details:

- `content` is a YAML document encoded as a JSON string. The server must store
  it without parsing, rewriting, or normalizing it.
- `last_used_with_version` is nullable.
- IDs are numeric.
- Dates must be valid JavaScript date strings. UTC RFC 3339 is compatible.
- The list endpoint must return a JSON array, not a paginated wrapper.
- New configs default to content `{}` in the original service.
- A missing name is accepted by the original service and replaced with an
  "Unnamed config" value.

## Synchronization behavior

When uploading, Tabby:

1. Downloads the current remote config.
2. Preserves locally disabled optional sections from the remote document.
3. Sends a `PATCH` containing `content` and `last_used_with_version`.
4. Records the returned `modified_at`.

When automatic sync is enabled, Tabby polls the selected config every 60
seconds and downloads it when the remote `modified_at` is newer than the
client's last observed value. The server must therefore update
`modified_at` on every successful mutation and return the updated object.

Tabby removes its own `configSync` section before upload and restores the local
section after download. Tokens are not expected inside stored config content.

## Security findings that affect the implementation

Configuration data is executable-equivalent. Tabby profiles can contain
commands and environment variables that are later launched by the terminal.
Tabby merged PR #11228 on 2026-05-07 to reject non-HTTPS sync hosts because a
man-in-the-middle attacker could alter YAML and obtain code execution.

Consequences:

- Public deployments must terminate TLS with a valid certificate.
- Tokens and config bodies must never be logged.
- Bearer tokens must be generated with a cryptographically secure RNG.
- This service stores only SHA-256 token digests, not recoverable tokens.
- Every config lookup includes the authenticated user ID to prevent
  cross-tenant access.
- Request bodies are bounded to limit memory and disk abuse.
- The service does not parse YAML, reducing the attack surface and preserving
  exact client data.

## Deliberate scope

Implemented:

- The six endpoints required by the Tabby desktop client.
- Bearer-token authentication.
- Multiple isolated users and multiple configs per user.
- Local administrative commands for user and token lifecycle.
- Health/readiness endpoints.

Not implemented:

- Browser terminal UI.
- OAuth or social login.
- Connection gateway and SSH/Telnet proxying.
- Tabby binary/version hosting.
- Session or cookie authentication.
- Public user registration.

`GET /api/1/user` intentionally does not echo the bearer token. The Tabby
desktop client only uses this endpoint as an authentication check, so omitting
the secret preserves compatibility while avoiding the original service's
unnecessary token disclosure.
