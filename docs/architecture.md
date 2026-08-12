# Architecture

## Trust and data boundaries

Public HTTPS reaches Caddy first. Caddy sends product UI, session, administration, health, and REST API routes to the Go service. Standards discovery, OAuth authorization, public JMAP, mail-client configuration, and ACME challenge routes go to Stalwart. Mail protocols reach Stalwart directly on their dedicated TLS-capable ports, so Stalwart obtains its own certificate for SMTP and IMAP while Caddy independently manages the public web certificate.

The branded sign-in form posts the password directly to same-origin `/api/auth`, which Caddy routes to Stalwart. The browser creates a PKCE verifier and sends only the one-time authorization code and verifier to MEOVV. The Go service exchanges that code, encrypts access and refresh tokens with AES-GCM, and returns an opaque Secure, HttpOnly, SameSite cookie. User passwords are never sent to or persisted by MEOVV.

Authenticated browser JMAP calls go through `/api/mail/jmap`; discovery goes through `/api/mail/session`; the event stream uses `/api/mail/events`. The Stalwart adapter is the only Go package that knows these upstream routes, which contains pre-1.0 compatibility changes.

## Persistence

Stalwart RocksDB/filesystem volumes contain:

- local principals, password hashes, application passwords, and authorization state
- message bodies, MIME source, attachments, mailboxes, search indexes, and queue state
- spam-filter data, DKIM material, and mail-server configuration

MEOVV SQLite in WAL mode contains:

- installation and product configuration
- hashed REST API keys and approved senders
- message envelope, size, submission, and per-recipient delivery state
- idempotency records with a 24-hour expiry
- webhook endpoints, encrypted-at-rest deployment secrets, attempts, and jobs
- encrypted OAuth tokens, sessions, preferences, and administrator audit events

The database deliberately has no message-body or attachment column.

## Delivery flow

REST submissions are validated, MIME-encoded, and written to SQLite in `queued` state in a single transaction with any idempotency record. The application then uses authenticated STARTTLS submission to Stalwart. It returns `202` only after Stalwart accepts the message. Stalwart is therefore the durable queue for every acknowledged submission.

Stalwart posts signed internal telemetry events. MEOVV normalizes these into `queued`, `processing`, `deferred`, `delivered`, `partial`, `bounced`, or `failed`, keeping recipient-level state. External webhook payloads are versioned independently from Stalwart event schemas.

## Compatibility

`release/compatibility.json` is signed with Ed25519. The release public key is compiled into `mailctl`; upgrade refuses a modified signature, a mismatched MEOVV release, or a different Stalwart version/image digest. Contract fixtures and adapter tests should be updated before signing a new manifest.
