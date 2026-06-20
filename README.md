# tabby-config-sync

[简体中文](README.zh-CN.md)

A small, security-focused configuration sync host for the
[Tabby terminal](https://github.com/Eugeny/tabby).

It implements only the API used by Tabby's desktop configuration sync feature.
It does not include a browser terminal, OAuth, a connection gateway, or
application distribution.

## Features

- Compatible with Tabby's built-in config sync client.
- Go service with SQLite persistence.
- Multiple isolated users and multiple configs per user.
- Bearer tokens stored only as SHA-256 digests.
- Transactional schema migrations and SQLite WAL mode.
- Strict JSON validation, request limits, timeouts, and structured logs.
- Non-root, read-only Docker deployment behind automatic HTTPS.
- Protocol-level integration tests and race-detector coverage.

See [the protocol research](docs/research.md) and
[architecture design](docs/design.md) for the upstream analysis and scope.

## Production deployment with Docker Compose

Requirements:

- A public DNS record pointing to the server.
- TCP ports 80 and 443 reachable from the internet.
- Docker with Compose.

Copy the environment template and set the public hostname:

```bash
cp .env.example .env
sed -i 's/sync.example.com/sync.your-domain.example/' .env
```

Build the image and create the first user:

```bash
docker compose build
docker compose run --rm app user create --name alice
```

The command prints the sync token once. Store it in a password manager.

Start the service:

```bash
docker compose up -d
docker compose ps
```

Caddy obtains and renews the TLS certificate automatically. HTTPS is not
optional for a public deployment: Tabby clients based on the security change
merged on May 7, 2026 reject cleartext sync hosts.

In Tabby, open **Settings → Config sync** and enter:

- Sync host: `https://sync.your-domain.example`
- Secret sync token: the generated `tcs_...` token

After the connection check succeeds, choose **Upload as a new config**. Enable
automatic sync only after verifying the initial upload/download direction.

## User administration

Run administrative commands with the same `/data` volume:

```bash
docker compose run --rm app user list
docker compose run --rm app user rotate-token --name alice
docker compose run --rm app user disable --name alice
docker compose run --rm app user enable --name alice
```

Rotating a token invalidates the previous token immediately. Disabling a user
preserves data while rejecting authentication.

## Local development

```bash
go test ./...
go test -race ./...
go run ./cmd/tabby-config-sync user create --name local
go run ./cmd/tabby-config-sync serve
```

The default development endpoint is `http://127.0.0.1:8080`, but current Tabby
clients require an HTTPS host. Use the Compose deployment or another trusted
TLS reverse proxy for end-to-end client testing.

## Creating a release

Releases are built by GitHub Actions. Push a semantic-version tag:

```bash
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

The Release workflow runs tests, builds Linux, macOS, and Windows archives for
AMD64 and ARM64, generates `checksums.txt`, and publishes all files to a GitHub
Release. Pre-release tags such as `v1.1.0-rc.1` are marked as pre-releases.

Validate the release configuration locally with:

```bash
make release-check
make release-snapshot
```

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `TCS_LISTEN_ADDRESS` | `:8080` | HTTP listen address |
| `TCS_DATABASE_PATH` | `./data/tabby-sync.db` | SQLite database path |
| `TCS_MAX_BODY_BYTES` | `8388608` | Maximum JSON request size |
| `TCS_READ_HEADER_TIMEOUT` | `5s` | Header read timeout |
| `TCS_READ_TIMEOUT` | `15s` | Full request read timeout |
| `TCS_WRITE_TIMEOUT` | `30s` | Response write timeout |
| `TCS_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `TCS_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `TCS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |

## Backup

The database is `/data/tabby-sync.db` in the application container. SQLite WAL
mode may also create `-wal` and `-shm` files. For a simple consistent backup,
stop the application briefly and copy the complete data volume:

```bash
docker compose stop app
docker run --rm \
  -v tabby-config-sync_sync-data:/source:ro \
  -v "$PWD/backups:/backup" \
  alpine:3.22 \
  sh -c 'cd /source && tar czf /backup/tabby-sync-$(date +%Y%m%d-%H%M%S).tar.gz .'
docker compose start app
```

Test restoration procedures before relying on backups.

## API

The compatibility API is documented in [api/openapi.yaml](api/openapi.yaml).
Health endpoints are:

- `GET /healthz`: process liveness.
- `GET /readyz`: database readiness.

## Security

Read [SECURITY.md](SECURITY.md) before exposing the service publicly. Most
importantly, do not publish the application port directly and do not terminate
TLS with an invalid or self-signed certificate unless every client explicitly
trusts that certificate.
