# Security model

## Primary controls

- Passwords and mailbox content stay in Stalwart.
- OAuth authorization code with PKCE prevents the browser from retaining a reusable password or bearer token.
- Access and refresh tokens are encrypted with AES-256-GCM and referenced by opaque HttpOnly session cookies.
- Mutating browser routes reject cross-site Origin and Fetch Metadata requests; cookies are SameSite and Secure on HTTPS.
- REST API keys are shown once, stored as SHA-256 digests, scoped, sender-bound, rate-limited, and individually revocable.
- REST messages are limited to 25 MiB decoded and 100 recipients; header names are allowlisted and CR/LF injection is rejected.
- Email HTML is sanitized, remote content is blocked by default, and attachment filenames are reduced to safe basenames.
- Webhooks require public HTTPS and reject loopback, private, link-local, unspecified, and multicast resolutions.
- External webhooks sign `{timestamp}.{raw_body}` with HMAC-SHA256 and retain event IDs across retries.
- Caddy adds HSTS; the application adds CSP, anti-framing, MIME-sniffing, referrer, and permissions policies.
- Only 25, 465, 587, and 993 are published for mail. POP3 and plaintext IMAP are absent.

## Deployment invariants

Before exposure, confirm that unauthenticated SMTP cannot relay to a third-party domain, sender authorization is enforced on submission, recovery administrator access is removed, and Stalwart's spam/phishing, SPF, DKIM, DMARC, TLS, abuse-ban, and rate-limit defaults remain enabled.

The internal Docker network is not a trust substitute. Stalwart delivery events use a separate shared HMAC secret. The Stalwart management API is not routed publicly by Caddy; administrators use the authenticated Go adapter.

## Audit and response

Administrator mutations write append-only application audit events. Production operators should additionally forward container logs to immutable external storage. Rotate an exposed API key or webhook secret rather than trying to recover it. Revoke active sessions and application passwords after an account incident.

## Required release review

Run open-relay, sender-spoofing, brute-force, CSRF, session-fixation, malicious HTML, attachment traversal, API-key leakage, and webhook SSRF tests for each supported architecture. Distribution also requires legal review of the Stalwart AGPL separation and corresponding-source offer.
