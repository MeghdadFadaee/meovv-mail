# Nginx and Certbot integration

Nginx and Certbot are host infrastructure. They are intentionally not part of
the MEOVV Compose project. Compose publishes two HTTP upstreams on loopback:

- `127.0.0.1:8080` — MEOVV web application and product API
- `127.0.0.1:8081` — Stalwart discovery, OAuth, JMAP, and autoconfiguration

Mail protocols remain directly exposed by Stalwart on 25, 465, 587, and 993.

## Automated host setup

On a fresh supported server, the repository installer performs the host steps
documented below without putting Nginx or Certbot inside Compose:

```bash
sudo ./scripts/install-server.sh install \
  --hostname mail.example.com \
  --email admin@example.com
```

Run it from the cloned repository root, or pass the repository's absolute path
with `--bundle-dir`. It supports Ubuntu 24.04, Ubuntu 26.04, and Debian 13.
Before running it, point the hostname's A/AAAA record at the server and allow
inbound port 80. The script obtains a certificate using Certbot's webroot flow,
enables the final route split, copies the certificate into MEOVV's protected
secrets directory, installs the renewal hook, and starts both containers.

It deliberately leaves DNS, PTR records, provider firewall rules, SSH, and UFW
to the operator unless the optional Cloudflare DNS prompt is accepted. It also
stops when it finds a conflicting unmanaged Nginx site or container package
rather than replacing an existing setup. After the browser wizard and permanent
administrator verification, run:

```bash
sudo ./scripts/install-server.sh finalize
```

The rest of this document describes the same integration for manual installs
and explains the files managed by the installer.

### Optional Cloudflare DNS setup

When accepted, the installer requests a Cloudflare API token with `Zone:Read`
and `DNS:Edit` through a hidden prompt. Before changing DNS, the installer shows
the selected zone, detected public addresses, and complete record plan for
confirmation.

The prompt links directly to the
[Cloudflare API Tokens dashboard](https://dash.cloudflare.com/profile/api-tokens).
Choose **Create Custom Token**, add `Zone → Zone → Read` and
`Zone → DNS → Edit`, and restrict **Zone Resources** to the selected zone. A
Global API Key is neither required nor recommended.

The token is used only for the confirmed DNS changes and is then discarded.
Certificate issuance and renewal deliberately use Certbot HTTP-01, proving that
the public hostname and port 80 reach this server without retaining Cloudflare
credentials.

Immediately before Certbot, the installer creates a random file under
`/var/www/certbot/.well-known/acme-challenge/` and requests it through the public
mail hostname without following redirects. It proceeds only when the response
is `200` with exact content. This detects unloaded Nginx site configuration,
incorrect public IP/NAT routing, canonical-host redirects, and intercepted port
80 traffic before consuming an ACME validation attempt. The report separates a
loopback request pinned to local Nginx from a normal public-hostname request and
includes public A/AAAA answers and any redirect location. If `sites-enabled` is
not loaded, the installer safely retries its managed site through
`/etc/nginx/conf.d/meovv-mail.conf`; it does not edit `nginx.conf` or unrelated
virtual hosts.

It manages the mail hostname's unproxied A/AAAA records, the zone MX record, and
the `autoconfig` and `autodiscover` CNAMEs. Existing SPF and DMARC policies are
preserved; conservative defaults are created only when no corresponding policy
exists. Multiple existing records that would make an update ambiguous stop the
installation for manual review.

Cloudflare cannot set the server provider's PTR record. DKIM is intentionally
deferred until the domain exists in Stalwart and its generated public key can be
published. For non-interactive use, pass `--configure-dns` and provide
`CLOUDFLARE_API_TOKEN` in the environment. Optional `--dns-zone`,
`--public-ipv4`, and `--public-ipv6` flags override detected defaults.

## Nginx

Copy `deploy/nginx/meovv-mail.conf.example` into the host's Nginx configuration,
replace `__MAIL_HOSTNAME__`, and change the two upstream ports if the bind values
in `.env` were customized. The routing split is required: sending every path to
MEOVV breaks OAuth/JMAP discovery, while sending every path to Stalwart hides the
MEOVV interface and REST API.

Validate and reload using the host's normal Nginx process. The example keeps
ACME HTTP-01 challenges under `/var/www/certbot`; it is compatible with Certbot's
webroot authenticator. If your existing Certbot integration uses the Nginx
authenticator, retain its generated challenge configuration instead.

The complete example references the final Certbot lineage and therefore should
be enabled only after that lineage exists. For a new hostname, obtain the first
certificate through your existing Nginx integration or a temporary port-80-only
challenge server, then enable the HTTPS server block.

Both Compose HTTP bindings default to `127.0.0.1`. Do not change them to a public
address. The Stalwart management/recovery endpoint must never be internet-facing.

## One certificate, two termination points

Nginx uses the Certbot certificate for HTTPS. Stalwart must use the same
certificate independently for SMTP submission and IMAP TLS. MEOVV mounts:

```text
secrets/tls/fullchain.pem -> /etc/stalwart/tls/fullchain.pem
secrets/tls/privkey.pem   -> /etc/stalwart/tls/privkey.pem
```

After Certbot has issued the hostname certificate, make the initial controlled
copy (replace the two paths as needed):

```bash
sudo env \
  MEOVV_BUNDLE_DIR=/opt/meovv-mail \
  RENEWED_LINEAGE=/etc/letsencrypt/live/mail.example.com \
  /opt/meovv-mail/deploy/certbot/deploy-hook.sh
```

Start the two containers and complete the MEOVV browser setup. Before removing
the temporary recovery account, register the mounted files with Stalwart:

```bash
docker compose up -d --build --remove-orphans
./mailctl configure-tls
./mailctl harden
```

`configure-tls` sends the recovery credential only to the loopback Stalwart
endpoint. It creates or updates a Stalwart `Certificate` object using file
references, selects it as `defaultCertificateId`, records the object id in
`secrets/tls/certificate_id`, and restarts Stalwart. If the Stalwart loopback
port was changed, pass a loopback URL explicitly:

```bash
./mailctl configure-tls --url http://127.0.0.1:9081/api
```

## Renewal hook

Install the hook in Certbot's renewal deploy-hook directory and set the bundle
location inside a small wrapper or Certbot service environment. For example:

```bash
sudo install -m 0755 \
  /opt/meovv-mail/deploy/certbot/deploy-hook.sh \
  /etc/letsencrypt/renewal-hooks/deploy/meovv-mail
```

The default hook expects the bundle at `/opt/meovv-mail`; set
`MEOVV_BUNDLE_DIR` if it lives elsewhere. Certbot supplies `RENEWED_LINEAGE`.
The hook copies only the certificate chain and private key, applies restrictive
permissions, and restarts Stalwart if it is running. When installed by
`install-server.sh`, a managed wrapper also validates and reloads Nginx after a
successful copy. Manual installations should retain their existing Nginx reload
hook or add an equivalent one.

Test the entire renewal path before production:

```bash
sudo certbot renew --dry-run
openssl s_client -connect mail.example.com:465 -servername mail.example.com </dev/null
openssl s_client -connect mail.example.com:993 -servername mail.example.com </dev/null
```

Confirm that HTTPS, SMTP submission, and IMAP present the same current hostname
certificate after the dry run.
