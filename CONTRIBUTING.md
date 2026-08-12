# Contributing

Thank you for helping improve MEOVV Mail.

## Before opening a change

- Search existing issues and pull requests.
- Use a discussion or feature request for substantial product or protocol
  changes before investing in an implementation.
- Use the private process in `SECURITY.md` for vulnerabilities.
- Never commit real domains, credentials, mailbox content, database files,
  backups, or customer logs.

The MEOVV product layer is currently published under an all-rights-reserved
license. Opening a pull request does not change ownership of your contribution
or grant the project additional rights. Maintainers may ask for a separate
contribution agreement before accepting code. Issue reports and documentation
corrections are welcome while that policy is finalized.

## Development checks

Install Node.js 22+, Go 1.24+, and Docker with Compose. Production deployments
also require host-managed Nginx and Certbot. Then run:

```bash
npm ci
npm run check
go test ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
go build ./cmd/...
MAIL_HOSTNAME=mail.example.com docker compose config --quiet
docker build -t meovv-mail:local .
```

Changes to Stalwart integration must also pass contract tests against the exact
image and digest in `compose.yaml`. Do not update that image independently of
the signed compatibility manifest.

## Pull requests

Keep changes focused, add tests for changed behavior, update public contracts
and operational documentation, and explain security or compatibility effects.
Generated output, local environment files, and secrets must remain untracked.
