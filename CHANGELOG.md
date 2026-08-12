# Changelog

All notable project changes will be recorded here. This project follows
[Semantic Versioning](https://semver.org/) after the first stable release.

## [Unreleased]

### Added

- Initial three-container self-hosted appliance architecture.
- React/Vite mailbox, setup, authentication, and administration interfaces.
- Go control plane, REST send API, JMAP session proxy, delivery events, and
  signed webhook delivery.
- SQLite state store, `mailctl` operations, signed compatibility manifest,
  OpenAPI contract, and operator documentation.

### Security

- OAuth authorization code flow with PKCE and encrypted server-side tokens.
- Hashed, scoped, sender-bound API keys and encrypted webhook secrets.
- HTML sanitization, remote-content blocking, CSRF defenses, webhook SSRF
  controls, and strict public protocol exposure.
