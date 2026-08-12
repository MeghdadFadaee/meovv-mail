# MEOVV Mail

![MEOVV Mail — private email, beautifully run](web/public/meovv-mail-social.png)

MEOVV Mail is a single-tenant, self-hosted email appliance for one organization.
It combines a polished web inbox and administration experience with a pinned Stalwart mail core,
a Go control plane, and automatic HTTPS through Caddy.

> [!IMPORTANT]
> MEOVV Mail is pre-1.0 software under active development. The architecture,
> UI, API contract, and local test suite are implemented, but a release must not
> be used for production correspondence until the Stalwart contract suite,
> open-relay/security review, supported-Linux restore drills, and documented
> performance acceptance tests pass for that exact release.

Version `0.1.0` targets one Linux host, multiple domains, and up to 500 local users.
It exposes SMTP on 25/465/587, IMAP over TLS on 993, JMAP and OAuth over HTTPS,
and a scoped REST send API. POP3 and plaintext IMAP are not published.

## Architecture

The production bundle runs exactly three containers:

- `caddy` terminates public TLS, publishes discovery routes, and sends product traffic to MEOVV.
- `meovv` serves the web application, encrypted OAuth sessions, REST API, setup, delivery metadata, webhook jobs, audit events, and operational endpoints.
- `stalwart` stores accounts and all mailbox data and provides SMTP, IMAP, JMAP, filtering, queues, and delivery telemetry.

Stalwart is pinned to `v0.16.17` and multi-architecture digest `sha256:a8108e19bd927e172d4d8c128907b8dfc93fd180ae8ee07dccdd42cb97eb9dfa`.
Mail bodies and attachments never enter the MEOVV SQLite database.

## Host requirements

- Ubuntu 24.04 or Debian 13 on amd64 or arm64
- Docker Engine with the Compose plugin
- 4 vCPU, 8 GB RAM, and SSD storage for the reference 500-account deployment
- A public static IP and control of DNS for direct delivery
- Inbound TCP 25, 80, 443, 465, 587, and 993
- Outbound TCP 25, or an authenticated TLS smart host

Direct delivery also needs working reverse DNS and acceptable IP reputation. If the hosting provider blocks port 25 or cannot set PTR, use relay mode.

## Install

Build the local operator utility, initialize the bundle, and start it:

```bash
go build -o mailctl ./cmd/mailctl
./mailctl init --hostname mail.example.com
docker compose up -d --build
```

`mailctl init` prints a one-time browser bootstrap token and temporary Stalwart recovery credential. Store both securely. Visit `https://mail.example.com`, complete the three-step wizard, and save the one-time result shown on the final screen.

After setup:

```bash
docker compose restart stalwart meovv
./mailctl doctor --hostname mail.example.com
```

Sign in with the permanent Stalwart administrator, create the dedicated `API_SUBMISSION_USER` account referenced by `.env`, put that account's application password in `secrets/api_submission_password`, verify REST submission, then remove the recovery backdoor:

```bash
./mailctl harden
```

Never leave `STALWART_RECOVERY_ADMIN` set on a production deployment. `mailctl reset-password` re-enables a temporary recovery credential during a lockout; reset and verify the permanent account, then immediately run `mailctl harden` again.

## DNS checklist

The dashboard and `mailctl doctor` cover the local checks. Before production traffic, verify:

- A and/or AAAA for the mail hostname
- MX for each hosted domain
- PTR from every sending IP back to the mail hostname
- SPF authorizing the appliance or relay
- Stalwart-generated DKIM records
- DMARC, initially in monitoring mode if desired
- HTTPS certificate and autoconfiguration discovery
- External inbound and outbound port 25 reachability

The Stalwart domain object contains the generated DNS zone material. Missing PTR, blocked outbound port 25, or poor IP reputation is a relay recommendation, not an appliance failure.

## REST API

The complete OpenAPI 3.1 contract is in [`api/openapi.yaml`](api/openapi.yaml). Administrator-created keys are displayed once, stored as SHA-256 digests, scoped to `messages.send` and/or `messages.status`, and restricted to approved sender identities.

```bash
curl https://mail.example.com/api/v1/messages \
  -H 'Authorization: Bearer meovv_REPLACE_ME' \
  -H 'Idempotency-Key: welcome-customer-1042' \
  -H 'Content-Type: application/json' \
  --data '{
    "from":"Notifications <notifications@example.com>",
    "to":["person@example.net"],
    "subject":"Welcome",
    "text":"Welcome to our service.",
    "html":"<p>Welcome to our service.</p>"
  }'
```

A successful submission returns `202 Accepted`. `delivered` means the destination mail server accepted the recipient; it does not mean a person read the message.

## Operations

Create a consistency-safe backup:

```bash
./mailctl backup
```

The command stops Stalwart and MEOVV writers, archives all five named volumes plus Compose configuration and secrets, writes SHA-256 checksums, and restarts the services. Restore is intentionally destructive and only accepts a backup from the same MEOVV and Stalwart release:

```bash
./mailctl restore --from backups/20260812T103000Z --yes
```

Upgrades verify the Ed25519 signature in `release/compatibility.sig`, reject unapproved Stalwart versions or digests, run diagnostics, and create a pre-upgrade backup:

```bash
./mailctl upgrade
```

Other useful commands:

```bash
./mailctl doctor
./mailctl version
docker compose exec meovv mailctl create-api-key \
  --name 'Production app' \
  --senders 'notifications@example.com,*@internal.example.com'
```

## Observability

- Liveness: `GET /health/live`
- Readiness: `GET /health/ready`
- Prometheus text format: `GET /metrics`
- Structured application logs: standard output
- Stalwart delivery diagnostics and queues: administrator dashboard through the authenticated adapter

Default retention is 30 days for delivery metadata, 7 days for webhook attempts, and 180 days for administrator audit events. Override with `MEOVV_MESSAGE_RETENTION`, `MEOVV_WEBHOOK_RETENTION`, and `MEOVV_AUDIT_RETENTION` duration values.

## Development and verification

```bash
npm ci
npm run dev
npm run check
go test ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
go build ./cmd/...
MAIL_HOSTNAME=mail.example.com docker compose config --quiet
docker build -t meovv-mail:local .
```

The standalone appliance UI is produced by `npm run build:web` in `web-dist`; the container image builds it automatically. See [`docs/architecture.md`](docs/architecture.md), [`docs/operations.md`](docs/operations.md), and [`docs/security.md`](docs/security.md) for system boundaries, production runbooks, and threat controls.

Pull requests run frontend checks, Go tests/vetting/builds, Compose validation,
container construction, and dependency review. See [`CONTRIBUTING.md`](CONTRIBUTING.md)
and report vulnerabilities through the private process in [`SECURITY.md`](SECURITY.md).

## Licensing

Stalwart is distributed unmodified as a separate AGPL service. MEOVV Mail does not link to its code. License notices and corresponding-source information are in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md). This repository is not a substitute for legal review before customer distribution.

The MEOVV product layer is currently **all rights reserved**; public source
availability is not an open-source license grant. See [`LICENSE`](LICENSE). If
the project adopts an open-source license later, update `LICENSE`, contribution
terms, package metadata, and release notices together.
