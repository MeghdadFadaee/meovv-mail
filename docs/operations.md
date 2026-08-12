# Operations runbook

## Delivery modes

Direct delivery is the default. Use it only when the host has a stable public IP, outbound TCP 25, correct PTR, and an IP range suitable for email. Relay mode sends through an authenticated TLS smart host and is the supported fallback. Changing mode does not require reinstalling; update the delivery configuration and run diagnostics before switching traffic.

## Backups and restore drills

`mailctl backup` stops the two data writers, archives every named volume and the host configuration/secrets, computes checksums, and starts the services again. Copy the resulting directory to storage that is encrypted, access-controlled, and separate from the mail host.

Run a restore drill for every release:

1. Prepare a clean host with the same MEOVV release.
2. Copy the complete backup directory to it.
3. Run `mailctl restore --from <directory> --yes`.
4. Run `mailctl doctor` and inspect `/health/ready`.
5. Verify administrator sign-in, mailbox counts, a historical attachment, REST status records, and DKIM output.
6. Submit inbound, direct/relay outbound, and webhook test messages.

Restore checks every archive before replacing any volume and rejects cross-release restores.

## Upgrades

Only use a complete versioned release bundle. `mailctl upgrade` verifies the compatibility signature, creates a backup, pulls approved images, builds the MEOVV image, and recreates the services. Never edit the Stalwart tag or digest directly. If preflight or signature validation fails, do not force the upgrade.

## Lockout recovery

`mailctl reset-password` creates or installs a temporary recovery administrator and recreates Stalwart. Use it to enter Stalwart, change the permanent account credential, revoke suspect sessions, and verify normal sign-in. Then run `mailctl harden`, which removes the recovery environment value and recreates Stalwart and MEOVV. Leaving recovery access enabled is a critical finding.

## Incident checks

- Queue growth: check DNS, upstream response text, outbound port 25, relay authentication, and reputation.
- Inbound failures: check public MX, TCP 25, Stalwart listeners, TLS, recipient existence, and bans.
- OAuth loops: confirm the browser hostname exactly matches `MAIL_HOSTNAME` and `STALWART_PUBLIC_URL`.
- Webhook retries: inspect DNS resolution, public HTTPS, response code, latency, and signature verification. Duplicates are expected after ambiguous failures.
- Readiness failure: inspect the named database volumes and Stalwart HTTPS health before restarting repeatedly.

## Retention

Cleanup runs daily. Delivery metadata defaults to 30 days, webhook attempts to 7 days, and administrator audit events to 180 days. Retention changes affect future cleanup and should be documented in the organization's policy.
