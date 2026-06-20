# Security policy and deployment requirements

## Reporting

Do not open a public issue containing tokens, synchronized configuration, or a
working exploit. Report vulnerabilities privately to the repository owner.

## Operational requirements

- Expose the service only through HTTPS with a valid certificate.
- Keep the application port private to the container or host network.
- Protect the Docker socket and host filesystem; either grants access to the
  SQLite database and synchronized credentials.
- Store sync tokens in a password manager.
- Rotate tokens after suspected disclosure.
- Back up the complete SQLite database state and test restores.
- Keep the container image and TLS proxy patched.

Tabby configuration is executable-equivalent. A modified profile can launch
commands on a client. Transport integrity is therefore a code-execution
boundary, not only a confidentiality concern.

## Implemented controls

- 256-bit random bearer tokens.
- Only SHA-256 token digests persisted.
- Per-user ownership checks on every config operation.
- No token or config-body logging.
- Request size limits and HTTP timeouts.
- Strict JSON decoding.
- Non-root application container with dropped capabilities and read-only root
  filesystem.
- No public registration, browser sessions, OAuth, or password database.

## Known boundaries

- Configuration content is encrypted only when the Tabby client uses its own
  Vault/encrypted configuration feature. The server otherwise stores content
  as plaintext inside SQLite.
- The service does not provide application-level end-to-end encryption.
- Possession of a sync token grants full read/write/delete access to that
  user's remote configs, matching the Tabby protocol.
