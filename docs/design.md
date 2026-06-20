# Architecture and security design

## Components

The service is one statically compiled Go process:

- `cmd/tabby-config-sync`: CLI and server lifecycle.
- `internal/database`: SQLite schema, migrations, users, tokens, and configs.
- `internal/httpapi`: protocol-compatible HTTP handlers and middleware.
- `internal/config`: environment configuration and validation.

TLS is terminated by Caddy in the production Compose example. The application
port remains private to the Compose network.

Production and CI builds are pinned to Go 1.26.4 or newer in the 1.26 release
line. This includes the June 2026 standard-library security fixes.

## Persistence

SQLite is configured with:

- foreign keys enabled;
- WAL journaling;
- a busy timeout;
- `synchronous=NORMAL`;
- schema migrations executed transactionally at startup.

Timestamps are stored as Unix milliseconds. Config updates use a timestamp at
least one millisecond greater than the previous value. This prevents Tabby's
JavaScript `Date` comparison from missing rapid consecutive changes.

## Authentication

Tokens are random 32-byte values encoded as URL-safe base64 and prefixed with
`tcs_`. Only a SHA-256 digest is stored. A token is displayed once when a user
is created or rotated.

Authentication failures return `401` and `WWW-Authenticate: Bearer`.
Authenticated config queries always constrain both config ID and user ID.
Disabled users cannot authenticate.

## HTTP controls

- Strict method/path routing.
- JSON-only mutation requests.
- Bounded request bodies.
- Rejection of unknown JSON fields.
- Read-header, read, write, idle, and graceful-shutdown timeouts.
- Security headers on all responses.
- Structured logs without request bodies or credentials.
- Stable JSON error objects.

The service trusts no forwarded headers for authorization or client identity.

## Operations

The binary includes:

- `serve`
- `user create`
- `user rotate-token`
- `user list`
- `user disable`
- `user enable`
- `healthcheck`
- `version`

Database files live under `/data` in the container. Operators should back up
the database and its WAL/SHM files together, or use a SQLite-aware backup
operation.

## Compatibility boundaries

The service matches the Tabby client's required contract, not every behavior
of Django REST Framework. In particular:

- unauthorized requests use standards-compliant `401` instead of depending on
  Django middleware ordering;
- validation errors use this service's stable error envelope;
- `/api/1/user` does not return the active bearer token;
- browser/OAuth endpoints are absent.
